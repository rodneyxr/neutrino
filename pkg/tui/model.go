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
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
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
	Component    string
	IsZarfVar    bool
	IsPkgVar     bool
	PkgVarDesc   string
	IsEmptyState bool
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
	pkgVariables     []v1alpha1.InteractiveVariable
}

func NewModel(report introspect.InspectionReport) Model {

	components := report.Components
	componentOrder := report.ComponentOrder
	pkgVariables := report.Variables

	// Initialize charts list in chronological definition order
	charts := make([]string, 0, len(componentOrder)+1)
	charts = append(charts, "All Charts")
	for _, k := range componentOrder {
		if _, ok := components[k]; ok {
			charts = append(charts, k)
		}
	}

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
		pkgVariables:    pkgVariables,
	}

	m.updateFilteredValues()
	return m
}

func (m *Model) updateFilteredValues() {
	var newValues []DisplayItem
	filter := m.filterInput.Value()

	// Add package variables unconditionally at the top if matches filter
	for _, pv := range m.pkgVariables {
		if filter == "" || strings.Contains(strings.ToLower(pv.Name), strings.ToLower(filter)) {
			desc := strings.ReplaceAll(pv.Description, "\n", " ")
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			newValues = append(newValues, DisplayItem{
				ValueUsage: introspect.ValueUsage{
					Path:         pv.Name,
					DefaultValue: pv.Default,
				},
				Component:  "Zarf Package Variables",
				IsPkgVar:   true,
				PkgVarDesc: desc,
			})
		}
	}

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

		chartHasItems := false

		for _, v := range res.Values {
			if filter == "" || strings.Contains(strings.ToLower(v.Path), strings.ToLower(filter)) {
				chartHasItems = true
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

		// Zarf Vars
		for _, zv := range res.ZarfVars {
			// Convert ZarfVariableUsage to ValueUsage structure for display
			v := introspect.ValueUsage{
				Path:         zv.Name,
				Chart:        name, // Zarf vars are package-level but associated with component here
				Wildcard:     false,
				Locations:    zv.Locations,
				DefaultValue: zv.DefaultValue,
			}

			if filter == "" || strings.Contains(strings.ToLower(v.Path), strings.ToLower(filter)) {
				chartHasItems = true
				item := DisplayItem{
					ValueUsage: v,
					Component:  name,
					IsZarfVar:  true,
				}
				if m.consolidateMode {
					newValues = append(newValues, item)
				} else {
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

		// Inject Empty State Dummy Row if zero items matched
		if !chartHasItems && filter == "" {
			newValues = append(newValues, DisplayItem{
				ValueUsage: introspect.ValueUsage{
					Path: "(No values discovered)",
				},
				Component:    name,
				IsEmptyState: true,
			})
		}
	}

	// Build map to track chronological component order
	compOrderIndex := make(map[string]int)
	for i, name := range m.chartNames {
		compOrderIndex[name] = i
	}

	sort.SliceStable(newValues, func(i, j int) bool {
		// Package Variables always go first
		if newValues[i].IsPkgVar && !newValues[j].IsPkgVar {
			return true
		}
		if !newValues[i].IsPkgVar && newValues[j].IsPkgVar {
			return false
		}

		// If both are package variables, they are already in correct chronological order
		// via iteration insertion. Return false to treat them as equal so SliceStable preserves their relative order.
		if newValues[i].IsPkgVar && newValues[j].IsPkgVar {
			return false
		}

		// Group by Component (chronological order)
		if newValues[i].Component != newValues[j].Component {
			return compOrderIndex[newValues[i].Component] < compOrderIndex[newValues[j].Component]
		}

		// Sort Zarf vars first
		if newValues[i].IsZarfVar != newValues[j].IsZarfVar {
			return newValues[i].IsZarfVar // True (Zarf) comes before False
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
						m.ensureCursorVisible()
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
						m.ensureCursorVisible()
					}
				} else if m.viewMode == ViewModeDetail {
					if m.detailCursor > 0 {
						m.detailCursor--
					}
				}
			case "g":
				// For simple 'g', go top.
				if m.viewMode == ViewModeList {
					m.cursor = 0
					m.viewportOffset = 0
				}
			case "G":
				if m.viewMode == ViewModeList {
					m.cursor = len(m.filteredValues) - 1
					m.ensureCursorVisible()
				}
			case "/":
				if m.viewMode == ViewModeList {
					m.inputMode = InputModeFilter
					m.filterInput.SetValue("") // Reset
					m.filterInput.Focus()
					m.updateFilteredValues()
					m.ensureCursorVisible()
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
					m.ensureCursorVisible()
					return m, nil
				}
			case "enter":
				if m.viewMode == ViewModeList && len(m.filteredValues) > 0 {
					if !m.filteredValues[m.cursor].IsEmptyState {
						m.viewMode = ViewModeDetail
						m.detailCursor = 0
					}
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
					m.ensureCursorVisible()
				}
			case "y":
				if m.viewMode == ViewModeList && len(m.filteredValues) > 0 {
					v := m.filteredValues[m.cursor]
					if v.IsEmptyState {
						return m, nil
					}
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
				m.chartInput.SetValue("")
				m.updateFilteredValues()
				m.cursor = 0
				m.ensureCursorVisible()
			case "esc":
				m.inputMode = InputModeNormal
				m.chartInput.Blur()
				m.currentChart = m.previousChart
				m.chartInput.SetValue("")
				m.updateFilteredValues()
				m.ensureCursorVisible()
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
					m.ensureCursorVisible()
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

// ensureCursorVisible adjusts viewportOffset to keep cursor in view.
// This replaces the recursive View() logic for performance.
func (m *Model) ensureCursorVisible() {
	if len(m.filteredValues) == 0 {
		return
	}

	// If cursor is above viewport, move viewport up
	if m.cursor < m.viewportOffset {
		m.viewportOffset = m.cursor
	}

	// If cursor is way below, we need to efficiently calculate where viewport should be.
	// Since items have variable height (due to headers), we can't just subtract windowHeight.
	// But we can iterate backwards from cursor?
	// Or simplistic approach: if cursor >= viewportOffset, check if it renders within maxLines.
	// If not, increment viewportOffset.

	maxLines := m.windowHeight - 5
	if maxLines < 1 {
		maxLines = 10
	}

	// Iterate from viewportOffset to cursor to count lines
	lines := 0
	lastComponent := ""
	lastIsZarfVar := false // Need to track this for Zarf vs Chart Values headers

	// Initialize lastComponent correctly based on context BEFORE viewportOffset?
	// This is tricky. If we just start counting from viewportOffset, we might miss context that caused a header.
	// But View() logic resets context at start loop anyway.

	// We only need to check if the cursor item is visible.
	// Optimization: If cursor is significantly larger than viewportOffset, bump viewportOffset
	// to at least (cursor - maxLines). This avoids scanning thousands of items.
	if m.cursor > m.viewportOffset+maxLines*2 {
		m.viewportOffset = m.cursor - maxLines
	}

	for {
		lines = 0
		lastComponent = ""
		// Need to see previous item to determine if we are at start of section
		if m.viewportOffset > 0 {
			lastComponent = m.filteredValues[m.viewportOffset-1].Component
			lastIsZarfVar = m.filteredValues[m.viewportOffset-1].IsZarfVar
		}

		for i := m.viewportOffset; i <= m.cursor; i++ {
			v := m.filteredValues[i]
			itemLines := 1 // Default item line

			// Check separators/headers
			if v.Component != lastComponent {
				// Chart Separator + Newline
				itemLines += 2
				// Header
				itemLines += 1

				lastComponent = v.Component
				lastIsZarfVar = v.IsZarfVar
			} else {
				// Same component. Check if we switched from Zarf to Values
				if lastIsZarfVar && !v.IsZarfVar {
					// We need a header for "Chart Values"
					itemLines += 1
				}
				lastIsZarfVar = v.IsZarfVar
			}

			// Are we at the very top (and just rendered a header implicit)?
			// Logic in View mirrors this.

			lines += itemLines
		}

		if lines <= maxLines {
			break // It fits!
		}

		m.viewportOffset++
		if m.viewportOffset > m.cursor {
			m.viewportOffset = m.cursor
			break
		}
	}
}

func (m Model) View() string {
	var s string

	if m.viewMode == ViewModeDetail {
		return m.detailView()
	}

	// Header
	header := titleStyle.Render("Neutrino")
	chartTitle := m.currentChart
	if chartTitle == "" {
		chartTitle = "All Charts"
		header += fmt.Sprintf(" - %s", chartTitle)
		s += header + "\n\n"
	} else if chartTitle == "All Charts" {
		header += fmt.Sprintf(" - %s", chartTitle)
		s += header + "\n\n"
	} else {
		// Specific chart - show metadata
		res := m.components[chartTitle]
		if res != nil {
			header += fmt.Sprintf(" - %s", chartTitle)
			header += fmt.Sprintf(" (%s)", res.ChartVersion)
			s += header + "\n"
			if res.RemoteSource != "" {
				s += helpStyle.Render("Source: "+res.RemoteSource) + "\n"
			} else {
				s += helpStyle.Render("Source: "+res.Source) + "\n"
			}
			s += "\n"
		} else {
			header += fmt.Sprintf(" - %s", chartTitle)
			s += header + "\n\n"
		}
	}

	// List
	var listContent string
	linesUsed := 0

	// Dynamically calculate maxLines based on the size of the header and footer
	linesInHeader := strings.Count(s, "\n")
	maxLines := m.windowHeight - linesInHeader - 3 // 3 for footer
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
	lastIsZarfVar := false

	if start > 0 {
		// Initialize context from previous item
		lastComponent = m.filteredValues[start-1].Component
		lastIsZarfVar = m.filteredValues[start-1].IsZarfVar
	}

	for i := start; i < len(m.filteredValues); i++ {
		v := m.filteredValues[i]
		itemStr := ""

		// Check for transition
		if v.Component != lastComponent {
			// New Component Section
			sepPrefix := "\n"
			if linesUsed == 0 {
				sepPrefix = "" // No leading newline for first item on screen
			}
			versionStr := ""
			if c, ok := m.components[v.Component]; ok {
				versionStr = fmt.Sprintf(" (%s)", c.ChartVersion)
			}
			itemStr += chartFilterStyle.Render(fmt.Sprintf("%s--- %s%s ---", sepPrefix, v.Component, versionStr)) + "\n"

			// Render Header dependent on first item type
			if v.IsPkgVar {
				headerStr := fmt.Sprintf("    %-50s %-20s %s", "VARIABLE", "DEFAULT", "DESCRIPTION")
				itemStr += tableHeaderStyle.Render(headerStr) + "\n"
			} else if v.IsZarfVar {
				headerStr := fmt.Sprintf("    %-50s %-20s %-10s %s", "ZARF VARIABLE", "DEFAULT", "TYPE", "LOCATION")
				itemStr += tableHeaderStyle.Render(headerStr) + "\n"
			} else {
				headerStr := fmt.Sprintf("    %-50s %-20s %-10s %s", "PATH", "DEFAULT", "TYPE", "LOCATION")
				itemStr += tableHeaderStyle.Render(headerStr) + "\n"
			}

			lastComponent = v.Component
			lastIsZarfVar = v.IsZarfVar
		} else {
			// Same Component, check if we switched from Zarf to Values
			if lastIsZarfVar && !v.IsZarfVar && !v.IsPkgVar {
				// Transition from Zarf -> Values
				// Render Values Header
				headerStr := fmt.Sprintf("\n    %-50s %-20s %-10s %s", "PATH", "DEFAULT", "TYPE", "LOCATION")
				itemStr += tableHeaderStyle.Render(headerStr) + "\n"
				lastIsZarfVar = v.IsZarfVar
			}
		}

		// Also check if we are at the very start of viewing (linesUsed==0) and missed the header logic
		// due to start > 0.
		// If start > 0, we initialized lastComponent/lastIsZarfVar.
		// But we might be in the middle of a list.
		// If we want sticky headers, we'd need to render them always if linesUsed==0.
		// But sticky headers are complex to implement without overlay.
		// Let's stick to "render header on transition".
		// BUT: If the user scrolls down, they lose the header.
		// "I would like the column headers to be on each table".
		// If the table is long, headers disappear.
		// For now, let's satisfy the layout requirement of separation.

		// If we are at the very top (linesUsed==0) and no transition occurred (because we are in middle of list),
		// we *could* render a header for context?
		// That might look duplicate if we scroll just past a header.
		// Let's rely on transitions for now.

		// Format columns
		pathCol := v.Path
		if len(pathCol) > 48 {
			pathCol = pathCol[:47] + "…"
		}

		defCol := formatDefaultValue(v.DefaultValue)
		if defCol == "-" {
			defCol = ""
		}
		if len(defCol) > 18 {
			defCol = defCol[:17] + "…"
		}

		typeCol := "inferred"
		if v.IsEmptyState {
			typeCol = "-"
		} else if v.IsZarfVar {
			typeCol = "variable"
		} else if len(v.Locations) > 0 {
			typeCol = "field"
		}

		locCol := ""
		if m.consolidateMode {
			if len(v.Locations) > 1 {
				locCol = fmt.Sprintf("(%d locs)", len(v.Locations))
			} else if len(v.Locations) == 1 {
				loc := v.Locations[0]
				file := loc.File
				if res, ok := m.components[v.Component]; ok && res.Source != "" {
					if rel, err := filepath.Rel(res.Source, file); err == nil {
						file = rel
					}
				}
				locCol = fmt.Sprintf("%s:%d", file, loc.Line)
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
				locCol = fmt.Sprintf("%s:%d", file, loc.Line)
			}
		}

		// Construct line
		var line string
		if v.IsPkgVar {
			line = fmt.Sprintf("%-50s %-20s %s", pathCol, defCol, helpStyle.Render(v.PkgVarDesc))
		} else if v.IsEmptyState {
			line = fmt.Sprintf("%-50s %-20s %-10s %s", pathCol, "", typeCol, "")
		} else {
			line = fmt.Sprintf("%-50s %-20s %-10s %s", pathCol, defCol, typeCol, locCol)
		}

		if i == m.cursor {
			itemStr += selectedItemStyle.Render(fmt.Sprintf("> %s", line)) + "\n"
		} else {
			if v.IsPkgVar || v.IsZarfVar {
				itemStr += itemStyle.Render(zarfVarStyle.Render(line)) + "\n"
			} else if v.IsEmptyState {
				// Gray out identical to description format
				itemStr += itemStyle.Render(helpStyle.Render(line)) + "\n"
			} else {
				itemStr += itemStyle.Render(line) + "\n"
			}
		}

		// Check if this item (including potential headers) fits
		itemLines := strings.Count(itemStr, "\n")
		// FIX: Do NOT recurse View(). Just stop rendering if full.
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

	label := "Value:"
	if v.IsZarfVar {
		label = "Zarf Variable:"
	}

	s := headerStyle.Render(label) + " " + v.Path + "\n"
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

func formatDefaultValue(val interface{}) string {
	if val == nil {
		return "-"
	}
	valBytes, _ := yaml.MarshalWithOptions(val, yaml.Flow(true))
	str := strings.TrimSpace(string(valBytes))
	str = strings.ReplaceAll(str, ".0,", ",")
	str = strings.ReplaceAll(str, ".0}", "}")
	str = strings.ReplaceAll(str, ".0]", "]")
	str = strings.TrimSuffix(str, ".0")
	if len(str) > 32 {
		return str[:29] + "..."
	}
	return str
}
