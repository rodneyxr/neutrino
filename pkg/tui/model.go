package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/goccy/go-yaml"
	"github.com/rodneyxr/neutrino/pkg/helm/introspect"
)

// ViewMode defines the current view state
type ViewMode int

const (
	ViewModeList ViewMode = iota
	ViewModeDetail
)

// InputMode defines the input state
type InputMode int

const (
	InputModeNormal InputMode = iota
	InputModeFilter
	InputModeChartSelect
)

type DisplayItem struct {
	introspect.ValueUsage
	Component string
}

type Model struct {
	// Data
	components     map[string]*introspect.IntrospectResult
	chartNames     []string
	currentChart   string
	filteredValues []DisplayItem

	// State
	viewMode        ViewMode
	inputMode       InputMode
	cursor          int
	detailCursor    int // For scrolling in detail view
	viewportOffset  int
	windowHeight    int
	windowWidth     int
	consolidateMode bool

	// Inputs
	filterInput      textinput.Model
	chartInput       textinput.Model
	chartSuggestions []string
	previousChart    string
	chartQuery       string
}

func NewModel(components map[string]*introspect.IntrospectResult) Model {

	// Initialize charts list
	charts := make([]string, 0, len(components)+1)
	charts = append(charts, "All Charts")
	for k := range components {
		charts = append(charts, k)
	}
	// Sort charts, keeping "All Charts" at top?
	// sort.Strings sorts alphabetically. "All Charts" comes before "podinfo" etc usually.
	// But let's ensure it.
	sort.Strings(charts)

	currentChart := "All Charts"

	// Initialize inputs
	fi := textinput.New()
	fi.Placeholder = "Filter values..."
	fi.Prompt = "/ "
	fi.ShowSuggestions = true

	ci := textinput.New()
	ci.Placeholder = "Switch chart..."
	ci.Prompt = ": "
	ci.ShowSuggestions = true

	m := Model{
		components:      components,
		chartNames:      charts,
		currentChart:    currentChart,
		viewMode:        ViewModeList,
		inputMode:       InputModeNormal,
		consolidateMode: true,
		filterInput:     fi,
		chartInput:      ci,
	}

	m.updateFilteredValues()
	return m
}

func (m *Model) updateFilteredValues() {
	var newValues []DisplayItem

	// filteredValues are derived from components based on currentChart and filterInput

	// Iterate valid chart names to maintain order and get keys
	for _, name := range m.chartNames {
		if name == "All Charts" {
			continue
		}

		// If viewing a specific chart, skip others
		if m.currentChart != "All Charts" && !strings.Contains(strings.ToLower(name), strings.ToLower(m.currentChart)) {
			continue
		}

		res := m.components[name]
		if res == nil {
			continue
		}

		filter := m.filterInput.Value()

		for _, v := range res.Values {
			if filter == "" || strings.Contains(strings.ToLower(v.Path), strings.ToLower(filter)) {
				// Create DisplayItem
				item := DisplayItem{
					ValueUsage: v,
					Component:  name,
				}

				if m.consolidateMode {
					newValues = append(newValues, item)
				} else {
					// Flatten locations
					if len(v.Locations) == 0 {
						newValues = append(newValues, item)
					} else {
						for _, loc := range v.Locations {
							val := item
							val.Locations = []introspect.ValueUsageLocation{loc}
							newValues = append(newValues, val)
						}
					}
				}
			}
		}
	}

	// Sort values - Group by Component, then Path
	sort.Slice(newValues, func(i, j int) bool {
		// Group by Component
		if newValues[i].Component != newValues[j].Component {
			return newValues[i].Component < newValues[j].Component
		}

		if newValues[i].Path == newValues[j].Path {
			// If paths are equal (unconsolidated), sort by file/line
			if len(newValues[i].Locations) > 0 && len(newValues[j].Locations) > 0 {
				locI := newValues[i].Locations[0]
				locJ := newValues[j].Locations[0]
				if locI.File != locJ.File {
					return locI.File < locJ.File
				}
				return locI.Line < locJ.Line
			}
		}
		return newValues[i].Path < newValues[j].Path
	})

	m.filteredValues = newValues

	// Set Autocomplete suggestions
	if m.inputMode == InputModeFilter {
		input := m.filterInput.Value()
		var suggestions []string
		if input != "" {
			for _, v := range newValues {
				if strings.HasPrefix(strings.ToLower(v.Path), strings.ToLower(input)) {
					// Add to suggestions if not already there (consolidated might have duplicates if we didn't unique them, but newValues should be unique paths in consolidated mode)
					// To be safe and keep it snappy, just take the first few distinct ones.
					alreadyAdded := false
					for _, s := range suggestions {
						if s == v.Path {
							alreadyAdded = true
							break
						}
					}
					if !alreadyAdded {
						suggestions = append(suggestions, v.Path)
					}
				}
				if len(suggestions) >= 5 {
					break
				}
			}
		}
		m.filterInput.SetSuggestions(suggestions)
	} else if m.inputMode == InputModeChartSelect {
		input := m.chartInput.Value()
		var suggestions []string
		for _, name := range m.chartNames {
			if name == "All Charts" {
				continue
			}
			if input == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(input)) {
				suggestions = append(suggestions, name)
			}
			if len(suggestions) >= 10 {
				break
			}
		}
		m.chartSuggestions = suggestions
		m.chartInput.SetSuggestions(suggestions)
	}

	// Reset cursor if out of bounds
	if m.cursor >= len(m.filteredValues) {
		m.cursor = 0
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.inputMode {
		case InputModeNormal:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "j", "down":
				if m.viewMode == ViewModeList {
					if m.cursor < len(m.filteredValues)-1 {
						m.cursor++
					}
				} else if m.viewMode == ViewModeDetail {
					v := m.filteredValues[m.cursor]
					if m.detailCursor < len(v.Locations)-1 {
						m.detailCursor++
					}
				}
			case "k", "up":
				if m.viewMode == ViewModeList {
					if m.cursor > 0 {
						m.cursor--
					}
				} else if m.viewMode == ViewModeDetail {
					if m.detailCursor > 0 {
						m.detailCursor--
					}
				}
			case "g":
				// Handle gg (need state? simplify: use Home/End for non-modal TUI or assume quick double press?
				// standard behavior: 'g' usually waits. 'Home' works.
				// Let's implement 'G' first. 'gg' is harder without state.
				// For simple 'g', go top.
				if m.viewMode == ViewModeList {
					m.cursor = 0
				}
			case "G":
				if m.viewMode == ViewModeList {
					m.cursor = len(m.filteredValues) - 1
				}
			case "/":
				if m.viewMode == ViewModeList {
					m.inputMode = InputModeFilter
					m.filterInput.SetValue("") // Reset
					m.filterInput.Focus()
					m.updateFilteredValues()
					return m, nil
				}
			case ":":
				if m.viewMode == ViewModeList {
					m.inputMode = InputModeChartSelect
					m.previousChart = m.currentChart
					m.chartQuery = ""
					m.chartInput.SetValue("") // Reset
					m.chartInput.Focus()
					m.updateFilteredValues()
					return m, nil
				}
			case "enter":
				if m.viewMode == ViewModeList && len(m.filteredValues) > 0 {
					m.viewMode = ViewModeDetail
					m.detailCursor = 0
				} else if m.viewMode == ViewModeDetail {
					// Open the selected location in detail view
					v := m.filteredValues[m.cursor]
					if len(v.Locations) > m.detailCursor {
						loc := v.Locations[m.detailCursor]
						return m, openEditor(loc.File, loc.Line, loc.Column)
					}
				}
			case "esc":
				if m.viewMode == ViewModeDetail {
					m.viewMode = ViewModeList
				} else if m.filterInput.Value() != "" {
					m.filterInput.SetValue("")
					m.updateFilteredValues()
					m.cursor = 0
				}
			case "y":
				if m.viewMode == ViewModeList && len(m.filteredValues) > 0 {
					v := m.filteredValues[m.cursor]
					if len(v.Locations) > 0 {
						loc := v.Locations[0]
						return m, openEditor(loc.File, loc.Line, loc.Column)
					}
				}
			case "c": // Toggle consolidate
				m.consolidateMode = !m.consolidateMode
				m.updateFilteredValues()
			}

		case InputModeFilter:
			switch msg.String() {
			case "enter":
				m.inputMode = InputModeNormal
				m.filterInput.Blur()
			case "esc":
				m.inputMode = InputModeNormal
				m.filterInput.Blur()
				m.filterInput.SetValue("")
				m.updateFilteredValues()
			default:
				m.filterInput, cmd = m.filterInput.Update(msg)
				m.updateFilteredValues()
				return m, cmd
			}

		case InputModeChartSelect:
			switch msg.String() {
			case "enter":
				// Confirm selection
				m.currentChart = m.chartInput.Value()
				if m.currentChart == "" {
					m.currentChart = "All Charts"
				}
				m.inputMode = InputModeNormal
				m.chartInput.Blur()
				m.chartInput.SetValue("")
				m.updateFilteredValues()
				m.cursor = 0
			case "esc":
				m.inputMode = InputModeNormal
				m.chartInput.Blur()
				m.currentChart = m.previousChart
				m.chartInput.SetValue("")
				m.updateFilteredValues()
			case "up", "down":
				// Local calculation of matches for the saved query
				query := strings.ToLower(m.chartQuery)
				var matches []string
				for _, name := range m.chartNames {
					if name == "All Charts" {
						continue
					}
					if query == "" || strings.Contains(strings.ToLower(name), query) {
						matches = append(matches, name)
					}
				}

				if len(matches) > 0 {
					// Find current index
					idx := -1
					for i, n := range matches {
						if strings.EqualFold(n, m.chartInput.Value()) {
							idx = i
							break
						}
					}

					if msg.String() == "up" {
						idx--
					} else {
						idx++
					}

					if idx < 0 {
						idx = len(matches) - 1
					} else if idx >= len(matches) {
						idx = 0
					}

					m.chartInput.SetValue(matches[idx])
					m.currentChart = matches[idx]
					m.updateFilteredValues()
				}
			default:
				m.chartInput, cmd = m.chartInput.Update(msg)
				m.chartQuery = m.chartInput.Value()
				m.currentChart = m.chartQuery
				if m.currentChart == "" {
					m.currentChart = "All Charts"
				}
				m.updateFilteredValues()
				return m, cmd
			}
		}

	case tea.WindowSizeMsg:
		m.windowHeight = msg.Height
		m.windowWidth = msg.Width
	}

	return m, nil
}

func (m Model) View() string {
	var s string

	if m.viewMode == ViewModeDetail {
		return m.detailView()
	}

	// Header
	header := titleStyle.Render("Neutrino TUI")
	chartTitle := m.currentChart
	if chartTitle == "" {
		chartTitle = "All Charts"
	}
	header += fmt.Sprintf(" - %s", chartTitle)
	s += header + "\n\n"

	// List
	var listContent string
	linesUsed := 0
	// 5 = Header(2) + Footer(3)
	maxLines := m.windowHeight - 5
	if maxLines < 1 {
		maxLines = 10
	}

	start := m.viewportOffset
	if m.cursor < start {
		m.viewportOffset = m.cursor
		start = m.cursor
	}

	// We'll iterate from start and stop when linesUsed >= maxLines
	lastComponent := ""
	if start > 0 {
		// Figure out lastComponent for the start item to avoid redundant headers
		// or just always show header for first item if start > 0?
		// Prefer being accurate.
		lastComponent = m.filteredValues[start-1].Component
	}

	for i := start; i < len(m.filteredValues); i++ {
		v := m.filteredValues[i]
		itemStr := ""

		// Chart/Component Separator
		if v.Component != lastComponent {
			sepPrefix := "\n"
			if linesUsed == 0 {
				sepPrefix = "" // No leading newline for first item on screen
			}
			itemStr += chartFilterStyle.Render(fmt.Sprintf("%s--- %s ---", sepPrefix, v.Component)) + "\n"
			lastComponent = v.Component
		}

		line := v.Path
		if m.consolidateMode {
			if len(v.Locations) > 1 {
				line += fmt.Sprintf(" (%d locs)", len(v.Locations))
			}
		} else {
			if len(v.Locations) > 0 {
				loc := v.Locations[0]
				file := loc.File
				if res, ok := m.components[v.Component]; ok && res.Source != "" {
					if rel, err := filepath.Rel(res.Source, file); err == nil {
						file = rel
					}
				}
				line += fmt.Sprintf(" %s:%d", file, loc.Line)
			}
		}

		if i == m.cursor {
			itemStr += selectedItemStyle.Render(fmt.Sprintf("> %s", line)) + "\n"
		} else {
			itemStr += itemStyle.Render(line) + "\n"
		}

		// Check if this item (including potential separator) fits
		itemLines := strings.Count(itemStr, "\n")
		if linesUsed+itemLines > maxLines && i <= m.cursor {
			// If we haven't reached the cursor yet, we must adjust viewport
			m.viewportOffset++
			return m.View() // Re-render with new offset
		}
		if linesUsed+itemLines > maxLines {
			break // Stop rendering
		}

		listContent += itemStr
		linesUsed += itemLines
	}

	s += listContent

	// Footer (Inputs or Status)
	s += "\n"
	if m.inputMode == InputModeFilter {
		s += m.filterInput.View()
	} else if m.inputMode == InputModeChartSelect {
		s += m.chartInput.View()
	} else {
		// Filter status
		if m.filterInput.Value() != "" {
			s += filterStyle.Render(fmt.Sprintf("Filter: %s", m.filterInput.Value())) + "\n"
		}

		// Help
		help := "filter (/) • chart (:) • details (enter) • quit (q)"
		s += helpStyle.Render(help)
	}

	return docStyle.Render(s)
}

func (m Model) detailView() string {
	if len(m.filteredValues) == 0 {
		return "No values."
	}
	v := m.filteredValues[m.cursor]

	s := headerStyle.Render("Value:") + " " + v.Path + "\n"
	s += headerStyle.Render("Component:") + " " + v.Component + "\n"
	if v.Chart != "" && v.Chart != v.Component {
		s += headerStyle.Render("Chart:") + " " + v.Chart + "\n"
	}
	s += "\n"

	s += headerStyle.Render("Default:") + "\n"
	valBytes, _ := yaml.Marshal(v.DefaultValue)
	valStr := strings.TrimSpace(string(valBytes))
	if valStr == "" || valStr == "{}" || valStr == "[]" || valStr == "null" {
		valStr = "-"
	} else {
		// Remove .0 from integers for consistency with table view
		valStr = strings.ReplaceAll(valStr, ".0,", ",")
		valStr = strings.ReplaceAll(valStr, ".0}", "}")
		valStr = strings.ReplaceAll(valStr, ".0]", "]")
		valStr = strings.TrimSuffix(valStr, ".0")
	}
	s += codeStyle.Render(valStr) + "\n\n"

	s += headerStyle.Render("Locations:") + "\n"
	for i, loc := range v.Locations {
		file := loc.File
		if res, ok := m.components[v.Component]; ok && res.Source != "" {
			if rel, err := filepath.Rel(res.Source, file); err == nil {
				file = rel
			}
		}

		locStr := fmt.Sprintf("%s %s",
			kindStyle.Render(fmt.Sprintf("[%s]", loc.Kind)),
			locationStyle.Render(fmt.Sprintf("%s:%d:%d", file, loc.Line, loc.Column)),
		)

		if i == m.detailCursor {
			s += selectedItemStyle.Render("> "+locStr) + "\n"
		} else {
			s += "  " + locStr + "\n"
		}
	}

	s += "\n" + helpStyle.Render("open (enter) • back (esc)")
	return docStyle.Render(s)
}

func openEditor(filename string, line, col int) tea.Cmd {
	// Try resolve absolute path if possible
	// Using model's sourceRoot or assuming filename is absolute?
	// The problem is we don't have access to model's sourceRoot here easily unless we pass it.
	// But `openEditor` is a helper.
	// Actually `neutrino inspect` --absolute resolves paths before passing to TUI.
	// If the user requested absolute paths, then filename is absolute.
	// If not, it is relative to CWD or Package Root.
	// We need to handle this.
	// Since we are in TUI, we might need to rely on the fact that `introspect.go` should ideally resolve absolute paths for TUI use regardless of flag display?
	// Or we pass Absolute paths to TUI model always.

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	// If path is not absolute, try to make it absolute?
	// But we don't know the root here.
	// Assume the caller passed a usable path.
	// See note below about ensuring absolute paths passed to model.

	var args []string
	if strings.Contains(editor, "code") {
		// VS Code: -g file:line:column
		args = []string{"-g", fmt.Sprintf("%s:%d:%d", filename, line, col)}
	} else {
		// Vim-style: +line and move to column
		args = []string{fmt.Sprintf("+%d", line), fmt.Sprintf("+normal! %dl", col-2), filename}
	}

	c := exec.Command(editor, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return nil
	})
}
