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
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/goccy/go-yaml"
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

// DetailItemKind distinguishes navigable items in the detail view.
type DetailItemKind int

const (
	DetailItemLocation DetailItemKind = iota
	DetailItemCrossRef
)

// DetailItem represents a single navigable row in the detail view.
type DetailItem struct {
	Kind     DetailItemKind
	Location introspect.ValueUsageLocation // valid when Kind == DetailItemLocation
	RefIndex int                           // index into filteredValues for cross-ref jumps
	Label    string                        // rendered label for cross-ref items
}

type DisplayItem struct {
	introspect.ValueUsage
	Component          string
	IsZarfVar          bool
	IsPkgVar           bool
	PkgVarDesc         string
	IsEmptyState       bool
	IsUDSOverrideValue bool
	IsUDSOverrideVar   bool
	IsSummaryItem      bool        // true for top-level UDS summary items
	OverrideValue      interface{} // The value being set (for override values)
	OriginalDefault    interface{} // The original chart default being overridden
	UDSVarPath         string      // The Helm path for UDS override variables
	UDSVarDescription  string      // Description for UDS override variables
	SummaryComponent   string      // source component for UDS summary items
}

type Model struct {
	// Data
	components           map[string]*introspect.IntrospectResult
	chartNames           []string
	currentChart         string
	filteredValues       []DisplayItem
	udsOverrideValues    []introspect.UDSOverrideValueWithContext
	udsOverrideVariables []introspect.UDSOverrideVariableWithContext

	// State
	viewMode            ViewMode
	inputMode           InputMode
	cursor              int
	detailCursor        int  // Navigable item cursor in detail view
	detailScrollOffset  int  // Line offset for viewport scrolling in detail view
	detailValueExpanded bool // Whether the default value block is expanded in detail view
	detailItems         []DetailItem
	viewportOffset      int
	windowHeight        int
	windowWidth         int
	consolidateMode     bool
	showZarfSummary     bool
	showUDSSummary      bool

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
		components:           components,
		chartNames:           charts,
		currentChart:         currentChart,
		viewMode:             ViewModeList,
		inputMode:            InputModeNormal,
		consolidateMode:      true,
		filterInput:          fi,
		chartInput:           ci,
		pkgVariables:         pkgVariables,
		udsOverrideValues:    report.UDSOverrideValues,
		udsOverrideVariables: report.UDSOverrideVariables,
	}

	m.updateFilteredValues()
	return m
}

func (m *Model) updateFilteredValues() {
	var newValues []DisplayItem
	filter := m.filterInput.Value()

	// Add package variables at the top if the Zarf summary toggle is on
	if m.showZarfSummary {
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
	}

	// Add UDS summary items if the UDS summary toggle is on
	if m.showUDSSummary {
		for _, ov := range m.udsOverrideValues {
			destComponent := fmt.Sprintf("%s/%s/%s", ov.PackageName, ov.ComponentName, ov.ChartName)
			if filter == "" || strings.Contains(strings.ToLower(ov.Path), strings.ToLower(filter)) {
				newValues = append(newValues, DisplayItem{
					ValueUsage: introspect.ValueUsage{
						Path:         ov.Path,
						DefaultValue: ov.OriginalDefault,
					},
					Component:          "UDS Bundle Overrides",
					IsUDSOverrideValue: true,
					IsSummaryItem:      true,
					OverrideValue:      ov.Value,
					OriginalDefault:    ov.OriginalDefault,
					SummaryComponent:   destComponent,
				})
			}
		}
		for _, ov := range m.udsOverrideVariables {
			destComponent := fmt.Sprintf("%s/%s/%s", ov.PackageName, ov.ComponentName, ov.ChartName)
			searchStr := strings.ToLower(ov.Name + " " + ov.Path)
			if filter == "" || strings.Contains(searchStr, strings.ToLower(filter)) {
				desc := strings.ReplaceAll(ov.Description, "\n", " ")
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				newValues = append(newValues, DisplayItem{
					ValueUsage: introspect.ValueUsage{
						Path:         ov.Name,
						DefaultValue: ov.Default,
					},
					Component:         "UDS Bundle Overrides",
					IsUDSOverrideVar:  true,
					IsSummaryItem:     true,
					UDSVarPath:        ov.Path,
					UDSVarDescription: desc,
					OriginalDefault:   ov.OriginalDefault,
					SummaryComponent:  destComponent,
				})
			}
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

		// UDS Override Values (hard-coded, not user-overridable)
		for _, ov := range res.UDSOverrideValues {
			if filter == "" || strings.Contains(strings.ToLower(ov.Path), strings.ToLower(filter)) {
				chartHasItems = true
				newValues = append(newValues, DisplayItem{
					ValueUsage: introspect.ValueUsage{
						Path:         ov.Path,
						DefaultValue: ov.OriginalDefault,
					},
					Component:          name,
					IsUDSOverrideValue: true,
					OverrideValue:      ov.Value,
					OriginalDefault:    ov.OriginalDefault,
				})
			}
		}

		// UDS Override Variables (user-overridable via uds-config.yaml)
		for _, ov := range res.UDSOverrideVariables {
			searchStr := strings.ToLower(ov.Name + " " + ov.Path)
			if filter == "" || strings.Contains(searchStr, strings.ToLower(filter)) {
				chartHasItems = true
				desc := strings.ReplaceAll(ov.Description, "\n", " ")
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				newValues = append(newValues, DisplayItem{
					ValueUsage: introspect.ValueUsage{
						Path:         ov.Name,
						DefaultValue: ov.Default,
					},
					Component:         name,
					IsUDSOverrideVar:  true,
					UDSVarPath:        ov.Path,
					UDSVarDescription: desc,
					OriginalDefault:   ov.OriginalDefault,
				})
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

		// UDS Bundle Overrides summary goes after pkg vars but before per-component items
		isSummaryI := newValues[i].IsSummaryItem
		isSummaryJ := newValues[j].IsSummaryItem
		if isSummaryI && !isSummaryJ {
			return true
		}
		if !isSummaryI && isSummaryJ {
			return false
		}
		if isSummaryI && isSummaryJ {
			// Within summary: values before variables
			if newValues[i].IsUDSOverrideValue != newValues[j].IsUDSOverrideValue {
				return newValues[i].IsUDSOverrideValue
			}
			return false // preserve insertion order
		}

		// Group by Component (chronological order)
		if newValues[i].Component != newValues[j].Component {
			return compOrderIndex[newValues[i].Component] < compOrderIndex[newValues[j].Component]
		}

		// Sort Zarf vars first, then UDS override values, then UDS override variables, then chart values
		if newValues[i].IsZarfVar != newValues[j].IsZarfVar {
			return newValues[i].IsZarfVar // True (Zarf) comes before False
		}

		if newValues[i].IsUDSOverrideValue != newValues[j].IsUDSOverrideValue {
			return newValues[i].IsUDSOverrideValue
		}

		if newValues[i].IsUDSOverrideVar != newValues[j].IsUDSOverrideVar {
			return newValues[i].IsUDSOverrideVar
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
					if m.detailCursor < len(m.detailItems)-1 {
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
				} else if m.viewMode == ViewModeDetail {
					m.detailScrollOffset = 0
				}
			case "G":
				if m.viewMode == ViewModeList {
					m.cursor = len(m.filteredValues) - 1
					m.ensureCursorVisible()
				} else if m.viewMode == ViewModeDetail {
					m.detailScrollOffset = 999999 // clamped to maxOffset in detailView
				}
			case "v":
				if m.viewMode == ViewModeDetail {
					m.detailValueExpanded = !m.detailValueExpanded
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
						m.detailScrollOffset = 0
						m.detailValueExpanded = false
						m.buildDetailItems()
					}
				} else if m.viewMode == ViewModeDetail {
					if m.detailCursor < len(m.detailItems) {
						di := m.detailItems[m.detailCursor]
						if di.Kind == DetailItemLocation {
							return m, openEditor(di.Location.File, di.Location.Line, di.Location.Column)
						} else if di.Kind == DetailItemCrossRef {
							// Jump to the referenced item's detail view
							if di.RefIndex >= 0 && di.RefIndex < len(m.filteredValues) {
								m.cursor = di.RefIndex
								m.detailCursor = 0
								m.detailScrollOffset = 0
								m.detailValueExpanded = false
								m.buildDetailItems()
								m.ensureCursorVisible()
							}
						}
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
			case "pgup":
				if m.viewMode == ViewModeList {
					maxLines := m.windowHeight - 5
					if maxLines < 1 {
						maxLines = 10
					}
					newCursor := m.cursor - maxLines
					if newCursor < 0 {
						newCursor = 0
					}
					m.cursor = newCursor
					m.viewportOffset = newCursor
					m.ensureCursorVisible()
				}
			case "pgdown":
				if m.viewMode == ViewModeList {
					maxLines := m.windowHeight - 5
					if maxLines < 1 {
						maxLines = 10
					}
					newCursor := m.cursor + maxLines
					if newCursor >= len(m.filteredValues) {
						newCursor = len(m.filteredValues) - 1
					}
					m.cursor = newCursor
					m.ensureCursorVisible()
				}
			case "J":
				if m.viewMode == ViewModeList {
					newCursor := m.cursor + 5
					if newCursor >= len(m.filteredValues) {
						newCursor = len(m.filteredValues) - 1
					}
					m.cursor = newCursor
					m.ensureCursorVisible()
				} else if m.viewMode == ViewModeDetail {
					m.detailScrollOffset++
				}
			case "K":
				if m.viewMode == ViewModeList {
					newCursor := m.cursor - 5
					if newCursor < 0 {
						newCursor = 0
					}
					m.cursor = newCursor
					m.ensureCursorVisible()
				} else if m.viewMode == ViewModeDetail {
					if m.detailScrollOffset > 0 {
						m.detailScrollOffset--
					}
				}
			case "n":
				if m.viewMode == ViewModeList {
					currentComponent := m.filteredValues[m.cursor].Component
					for i := m.cursor + 1; i < len(m.filteredValues); i++ {
						if m.filteredValues[i].Component != currentComponent {
							m.cursor = i
							m.ensureCursorVisible()
							break
						}
					}
					m.viewportOffset = m.cursor
					m.ensureCursorVisible()
				}
			case "b":
				if m.viewMode == ViewModeList {
					if len(m.filteredValues) == 0 {
						break
					}

					currentComponent := m.filteredValues[m.cursor].Component
					// Find the first index of the current component
					startIdx := m.cursor
					for startIdx > 0 && m.filteredValues[startIdx-1].Component == currentComponent {
						startIdx--
					}

					// If already at the very first component, stay at top
					if startIdx == 0 {
						m.cursor = 0
					} else {
						// previous component is at startIdx-1; find its first index
						prevIdx := startIdx - 1
						prevComp := m.filteredValues[prevIdx].Component
						for prevIdx > 0 && m.filteredValues[prevIdx-1].Component == prevComp {
							prevIdx--
						}
						m.cursor = prevIdx
					}
					m.viewportOffset = m.cursor
					m.ensureCursorVisible()
				}
			case "c": // Toggle consolidate
				m.consolidateMode = !m.consolidateMode
				m.updateFilteredValues()
			case "Z": // Toggle Zarf package variable summary
				m.showZarfSummary = !m.showZarfSummary
				m.updateFilteredValues()
				m.cursor = 0
				m.viewportOffset = 0
			case "U": // Toggle UDS override summary
				m.showUDSSummary = !m.showUDSSummary
				m.updateFilteredValues()
				m.cursor = 0
				m.viewportOffset = 0
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
	lastSectionType := 0 // Track section type for header transitions

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
			lastSectionType = sectionTypeOf(m.filteredValues[m.viewportOffset-1])
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
				lastSectionType = sectionTypeOf(v)
			} else {
				// Same component. Check if section type changed
				curSection := sectionTypeOf(v)
				if curSection != lastSectionType {
					// Newline + header for new section
					itemLines += 2
					lastSectionType = curSection
				}
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

// renderValueBlock renders a labeled value block, collapsed to a single preview line
// (with a hint to expand) unless expanded is true.
func renderValueBlock(label, valStr string, expanded bool) string {
	lines := strings.Split(valStr, "\n")
	if len(lines) <= 1 {
		// Single-line value — always show inline, no toggle needed.
		return headerStyle.Render(label) + "\n" + codeStyle.Render(valStr) + "\n"
	}
	if expanded {
		return headerStyle.Render(label) + helpStyle.Render(" (v to collapse)") + "\n" + codeStyle.Render(valStr) + "\n"
	}
	// Collapsed: show first line with count hint.
	moreLines := len(lines) - 1
	return headerStyle.Render(label) + "\n" +
		codeStyle.Render(lines[0]) + helpStyle.Render(fmt.Sprintf(" ... +%d lines (v to expand)", moreLines)) + "\n"
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
	lastSectionType := 0 // 0=unknown, 1=pkgVar, 2=zarfVar, 3=udsOverrideValue, 4=udsOverrideVar, 5=chartValue

	if start > 0 {
		// Initialize context from previous item
		lastComponent = m.filteredValues[start-1].Component
		lastSectionType = sectionTypeOf(m.filteredValues[start-1])
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
			itemStr += sectionHeader(v) + "\n"

			lastComponent = v.Component
			lastSectionType = sectionTypeOf(v)
		} else {
			// Same Component, check if section type changed
			curSection := sectionTypeOf(v)
			if curSection != lastSectionType {
				itemStr += "\n" + sectionHeader(v) + "\n"
				lastSectionType = curSection
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
		} else if v.IsUDSOverrideValue || v.IsUDSOverrideVar {
			typeCol = "override"
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
		} else if v.IsSummaryItem && v.IsUDSOverrideValue {
			valCol := formatDefaultValue(v.OverrideValue)
			if len(valCol) > 18 {
				valCol = valCol[:17] + "…"
			}
			origCol := formatDefaultValue(v.OriginalDefault)
			if origCol == "-" {
				origCol = ""
			}
			if len(origCol) > 18 {
				origCol = origCol[:17] + "…"
			}
			compCol := v.SummaryComponent
			if len(compCol) > 30 {
				compCol = compCol[:29] + "…"
			}
			line = fmt.Sprintf("%-50s %-20s %-20s %s", pathCol, valCol, origCol, helpStyle.Render(compCol))
		} else if v.IsSummaryItem && v.IsUDSOverrideVar {
			udsPathCol := v.UDSVarPath
			if len(udsPathCol) > 28 {
				udsPathCol = udsPathCol[:27] + "…"
			}
			compCol := v.SummaryComponent
			if len(compCol) > 18 {
				compCol = compCol[:17] + "…"
			}
			descCol := v.UDSVarDescription
			line = fmt.Sprintf("%-50s %-30s %-20s %s", pathCol, udsPathCol, compCol, helpStyle.Render(descCol))
		} else if v.IsUDSOverrideValue {
			valCol := formatDefaultValue(v.OverrideValue)
			if len(valCol) > 18 {
				valCol = valCol[:17] + "…"
			}
			origCol := formatDefaultValue(v.OriginalDefault)
			if origCol == "-" {
				origCol = ""
			}
			if len(origCol) > 18 {
				origCol = origCol[:17] + "…"
			}
			line = fmt.Sprintf("%-50s %-20s %s", pathCol, valCol, origCol)
		} else if v.IsUDSOverrideVar {
			udsPathCol := v.UDSVarPath
			if len(udsPathCol) > 28 {
				udsPathCol = udsPathCol[:27] + "…"
			}
			descCol := v.UDSVarDescription
			// Match widths with UDS Override Path columns (50,20)
			line = fmt.Sprintf("%-50s %-30s %s", pathCol, udsPathCol, helpStyle.Render(descCol))
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
			} else if v.IsUDSOverrideValue {
				itemStr += itemStyle.Render(udsOverrideValueStyle.Render(line)) + "\n"
			} else if v.IsUDSOverrideVar {
				itemStr += itemStyle.Render(udsOverrideVarStyle.Render(line)) + "\n"
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
		help := "filter (/) • components (:) • details (enter) • jump (n/b) • zarf vars (Z) • uds vars (U) • quit (q)"
		s += helpStyle.Render(help)
	}

	return docStyle.Render(s)
}

// findCrossRefs finds UDS overrides/variables that target a given Helm path within a component,
// and template locations where a path is used. Returns detail items for cross-reference display.
func (m Model) findOverridesForPath(component, path string) []DetailItem {
	var items []DetailItem
	res := m.components[component]
	if res == nil {
		return items
	}
	for _, ov := range res.UDSOverrideValues {
		if ov.Path == path {
			// Find matching DisplayItem in filteredValues
			for idx, di := range m.filteredValues {
				if di.IsUDSOverrideValue && !di.IsSummaryItem && di.Component == component && di.Path == path {
					items = append(items, DetailItem{
						Kind:     DetailItemCrossRef,
						RefIndex: idx,
						Label:    fmt.Sprintf("→ UDS Override Value: %s = %s", ov.Path, formatDefaultValue(ov.Value)),
					})
					break
				}
			}
		}
	}
	for _, ov := range res.UDSOverrideVariables {
		if ov.Path == path {
			for idx, di := range m.filteredValues {
				if di.IsUDSOverrideVar && !di.IsSummaryItem && di.Component == component && di.UDSVarPath == path {
					if di.Path == ov.Name {
						items = append(items, DetailItem{
							Kind:     DetailItemCrossRef,
							RefIndex: idx,
							Label:    fmt.Sprintf("→ UDS Variable: %s → %s", ov.Name, ov.Path),
						})
						break
					}
				}
			}
		}
	}
	return items
}

// findLocationsForOverridePath finds the Helm value template locations for a given path in a component.
func (m Model) findLocationsForOverridePath(component, path string) []DetailItem {
	var items []DetailItem
	res := m.components[component]
	if res == nil {
		return items
	}
	for _, v := range res.Values {
		if v.Path == path {
			for _, loc := range v.Locations {
				items = append(items, DetailItem{
					Kind:     DetailItemLocation,
					Location: loc,
				})
			}
			// Also add a cross-ref link to the value item itself
			for idx, di := range m.filteredValues {
				if !di.IsZarfVar && !di.IsUDSOverrideValue && !di.IsUDSOverrideVar && !di.IsPkgVar && !di.IsSummaryItem && di.Component == component && di.Path == path {
					items = append(items, DetailItem{
						Kind:     DetailItemCrossRef,
						RefIndex: idx,
						Label:    fmt.Sprintf("→ Helm Value: %s (%d locations)", path, len(v.Locations)),
					})
					break
				}
			}
			break
		}
	}
	return items
}

// buildDetailItems constructs the unified navigable list for the detail view of the current item.
func (m *Model) buildDetailItems() {
	if len(m.filteredValues) == 0 {
		m.detailItems = nil
		return
	}
	v := m.filteredValues[m.cursor]
	var items []DetailItem

	if v.IsUDSOverrideValue || v.IsUDSOverrideVar {
		// For UDS overrides, show template locations where the overridden path is used
		targetPath := v.Path
		if v.IsUDSOverrideVar {
			targetPath = v.UDSVarPath
		}
		targetComponent := v.Component
		if v.IsSummaryItem {
			targetComponent = v.SummaryComponent
		}
		items = append(items, m.findLocationsForOverridePath(targetComponent, targetPath)...)
	} else if v.IsPkgVar {
		// No cross-refs for package variables currently
	} else {
		// Regular Helm value or Zarf var: show locations, then cross-refs to UDS overrides
		for _, loc := range v.Locations {
			items = append(items, DetailItem{
				Kind:     DetailItemLocation,
				Location: loc,
			})
		}
		if !v.IsZarfVar {
			items = append(items, m.findOverridesForPath(v.Component, v.Path)...)
		}
	}

	m.detailItems = items
}

func (m Model) detailView() string {
	if len(m.filteredValues) == 0 {
		return "No values."
	}
	v := m.filteredValues[m.cursor]

	// --- Header section ---
	label := "Value:"
	if v.IsZarfVar {
		label = "Zarf Variable:"
	} else if v.IsUDSOverrideValue {
		label = "UDS Override Value:"
	} else if v.IsUDSOverrideVar {
		label = "UDS Override Variable:"
	}

	s := headerStyle.Render(label) + " " + v.Path + "\n"
	s += headerStyle.Render("Component:") + " " + v.Component + "\n"
	if v.Chart != "" && v.Chart != v.Component {
		s += headerStyle.Render("Chart:") + " " + v.Chart + "\n"
	}
	if v.IsSummaryItem && v.SummaryComponent != "" {
		s += headerStyle.Render("Target Component:") + " " + v.SummaryComponent + "\n"
	}
	if v.IsUDSOverrideVar && v.UDSVarPath != "" {
		s += headerStyle.Render("Helm Path:") + " " + v.UDSVarPath + "\n"
	}
	if v.IsUDSOverrideVar && v.UDSVarDescription != "" {
		s += headerStyle.Render("Description:") + " " + v.UDSVarDescription + "\n"
	}
	s += "\n"

	// --- Locations / cross-references (shown first so they are immediately visible) ---
	hasLocations := false
	hasCrossRefs := false
	for _, di := range m.detailItems {
		if di.Kind == DetailItemLocation {
			hasLocations = true
		} else {
			hasCrossRefs = true
		}
	}

	if hasLocations {
		if v.IsUDSOverrideValue || v.IsUDSOverrideVar {
			s += headerStyle.Render("Affected Template Locations:") + "\n"
		} else {
			s += headerStyle.Render("Locations:") + "\n"
		}
	}

	lastKind := DetailItemLocation
	for i, di := range m.detailItems {
		if di.Kind == DetailItemCrossRef && lastKind == DetailItemLocation && hasLocations {
			if v.IsUDSOverrideValue || v.IsUDSOverrideVar {
				s += "\n" + headerStyle.Render("Related Values:") + "\n"
			} else {
				s += "\n" + headerStyle.Render("Overrides:") + "\n"
			}
		}
		lastKind = di.Kind

		if di.Kind == DetailItemLocation {
			file := di.Location.File
			compKey := v.Component
			if v.IsSummaryItem {
				compKey = v.SummaryComponent
			}
			if res, ok := m.components[compKey]; ok && res.Source != "" {
				if rel, err := filepath.Rel(res.Source, file); err == nil {
					file = rel
				}
			}
			locStr := fmt.Sprintf("%s %s",
				kindStyle.Render(fmt.Sprintf("[%s]", di.Location.Kind)),
				locationStyle.Render(fmt.Sprintf("%s:%d:%d", file, di.Location.Line, di.Location.Column)),
			)
			if i == m.detailCursor {
				s += selectedItemStyle.Render("> "+locStr) + "\n"
			} else {
				s += "  " + locStr + "\n"
			}
		} else {
			if i == m.detailCursor {
				s += selectedItemStyle.Render("> "+di.Label) + "\n"
			} else {
				s += "  " + crossRefStyle.Render(di.Label) + "\n"
			}
		}
	}

	if !hasCrossRefs && !hasLocations && !v.IsPkgVar {
		s += helpStyle.Render("(no template locations found)") + "\n"
	}

	// --- Default / override value (collapsible, below locations) ---
	s += "\n"
	if v.IsUDSOverrideValue {
		valBytes, _ := yaml.Marshal(v.OverrideValue)
		valStr := strings.TrimSpace(string(valBytes))
		if valStr == "" || valStr == "null" {
			valStr = "-"
		}
		s += renderValueBlock("Override Value:", valStr, m.detailValueExpanded)

		origBytes, _ := yaml.Marshal(v.OriginalDefault)
		origStr := strings.TrimSpace(string(origBytes))
		if origStr == "" || origStr == "{}" || origStr == "[]" || origStr == "null" {
			origStr = "-"
		}
		s += renderValueBlock("Original Default:", origStr, m.detailValueExpanded)
	} else if v.IsUDSOverrideVar {
		valBytes, _ := yaml.Marshal(v.DefaultValue)
		valStr := strings.TrimSpace(string(valBytes))
		if valStr == "" || valStr == "{}" || valStr == "[]" || valStr == "null" {
			valStr = "-"
		}
		s += renderValueBlock("Default:", valStr, m.detailValueExpanded)

		origBytes, _ := yaml.Marshal(v.OriginalDefault)
		origStr := strings.TrimSpace(string(origBytes))
		if origStr == "" || origStr == "{}" || origStr == "[]" || origStr == "null" {
			origStr = "-"
		}
		s += renderValueBlock("Original Default:", origStr, m.detailValueExpanded)
	} else {
		valBytes, _ := yaml.Marshal(v.DefaultValue)
		valStr := strings.TrimSpace(string(valBytes))
		if valStr == "" || valStr == "{}" || valStr == "[]" || valStr == "null" {
			valStr = "-"
		} else {
			valStr = strings.ReplaceAll(valStr, ".0,", ",")
			valStr = strings.ReplaceAll(valStr, ".0}", "}")
			valStr = strings.ReplaceAll(valStr, ".0]", "]")
			valStr = strings.TrimSuffix(valStr, ".0")
		}
		s += renderValueBlock("Default:", valStr, m.detailValueExpanded)
	}

	// --- Snippets ---
	if !v.IsZarfVar && !v.IsUDSOverrideValue && !v.IsUDSOverrideVar && !v.IsPkgVar && !v.IsEmptyState {
		compKey := v.Component
		snippet := generateBundleSnippet(compKey, v.Path, v.DefaultValue)
		if snippet != "" {
			s += "\n" + snippetLabelStyle.Render("UDS Bundle Override Snippet:") + "\n"
			s += snippetStyle.Render(snippet) + "\n"
		}
	}

	if v.IsUDSOverrideVar {
		compKey := v.Component
		if v.IsSummaryItem {
			compKey = v.SummaryComponent
		}
		snippet := generateConfigSnippet(compKey, v.Path)
		if snippet != "" {
			s += "\n" + snippetLabelStyle.Render("UDS Config Snippet:") + "\n"
			s += snippetStyle.Render(snippet) + "\n"
		}
	}

	// --- Viewport clipping ---
	viewHeight := m.windowHeight - 2
	if viewHeight < 1 {
		viewHeight = m.windowHeight
	}

	lines := strings.Split(s, "\n")
	totalLines := len(lines)

	if totalLines <= viewHeight {
		m.detailScrollOffset = 0
	} else {
		maxOffset := totalLines - viewHeight
		if m.detailScrollOffset > maxOffset {
			m.detailScrollOffset = maxOffset
		}
		if m.detailScrollOffset < 0 {
			m.detailScrollOffset = 0
		}
	}

	end := m.detailScrollOffset + viewHeight
	if end > totalLines {
		end = totalLines
	}

	clipped := strings.Join(lines[m.detailScrollOffset:end], "\n")

	scrollHint := ""
	if totalLines > viewHeight {
		scrollPct := 0
		if totalLines-viewHeight > 0 {
			scrollPct = m.detailScrollOffset * 100 / (totalLines - viewHeight)
		}
		scrollHint = helpStyle.Render(fmt.Sprintf(" scroll (J/K • g/G) %d%%", scrollPct)) + "  "
	}

	help := scrollHint + helpStyle.Render("expand value (v) • open/jump (enter) • back (esc)")
	return docStyle.Render(clipped) + "\n" + help
}

// parseComponentKey splits a component key into its parts.
// UDS keys are "pkg/component/chart" (3 parts), Zarf keys are "component/chart" (2 parts).
func parseComponentKey(key string) (pkgName, componentName, chartName string) {
	parts := strings.Split(key, "/")
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	default:
		return "", "", key
	}
}

// helmPathToVarName converts a Helm values path like "global.proxy.resources.requests.memory"
// to an uppercase variable name like "GLOBAL_PROXY_RESOURCES_REQUESTS_MEMORY".
func helmPathToVarName(path string) string {
	name := strings.ReplaceAll(path, ".", "_")
	name = strings.ReplaceAll(name, "-", "_")
	return strings.ToUpper(name)
}

// generateBundleSnippet generates a uds-bundle.yaml snippet showing how to create
// a UDS override variable for a given Helm value path.
func generateBundleSnippet(componentKey, helmPath string, defaultValue interface{}) string {
	pkgName, componentName, chartName := parseComponentKey(componentKey)
	if pkgName == "" {
		// Not a UDS bundle context (Zarf or plain Helm)
		return ""
	}
	varName := helmPathToVarName(helmPath)
	defaultStr := "null"
	if defaultValue != nil {
		b, _ := yaml.MarshalWithOptions(defaultValue, yaml.Flow(true))
		defaultStr = strings.TrimSpace(string(b))
		if defaultStr == "" {
			defaultStr = "null"
		}
	}
	return fmt.Sprintf(`# uds-bundle.yaml
packages:
  - name: %s
    overrides:
      %s:
        %s:
          variables:
            - name: <prefix>_%s
              path: %s
              description: "<description>"
              default: %s`, pkgName, componentName, chartName, varName, helmPath, defaultStr)
}

// generateConfigSnippet generates a uds-config.yaml snippet showing how to set
// a UDS override variable value.
func generateConfigSnippet(componentKey, varName string) string {
	pkgName, _, _ := parseComponentKey(componentKey)
	if pkgName == "" {
		return ""
	}
	return fmt.Sprintf(`# uds-config.yaml
variables:
  %s:
    %s: <value>`, pkgName, varName)
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

// sectionTypeOf returns the section type of a DisplayItem for header transition tracking.
// 1=pkgVar, 2=zarfVar, 3=udsOverrideValue, 4=udsOverrideVar, 5=chartValue,
// 6=udsSummaryValue, 7=udsSummaryVar
func sectionTypeOf(v DisplayItem) int {
	if v.IsPkgVar {
		return 1
	}
	if v.IsSummaryItem && v.IsUDSOverrideValue {
		return 6
	}
	if v.IsSummaryItem && v.IsUDSOverrideVar {
		return 7
	}
	if v.IsZarfVar {
		return 2
	}
	if v.IsUDSOverrideValue {
		return 3
	}
	if v.IsUDSOverrideVar {
		return 4
	}
	return 5
}

// sectionHeader builds the table header string for a given DisplayItem type.
func sectionHeader(v DisplayItem) string {
	var headerStr string
	switch {
	case v.IsPkgVar:
		headerStr = fmt.Sprintf("    %-50s %-20s %s", "VARIABLE", "DEFAULT", "DESCRIPTION")
	case v.IsSummaryItem && v.IsUDSOverrideValue:
		headerStr = fmt.Sprintf("    %-50s %-20s %-20s %s", "UDS OVERRIDE PATH", "VALUE", "ORIGINAL DEFAULT", "COMPONENT")
	case v.IsSummaryItem && v.IsUDSOverrideVar:
		headerStr = fmt.Sprintf("    %-50s %-30s %-20s %s", "UDS VARIABLE", "PATH", "COMPONENT", "DESCRIPTION")
	case v.IsZarfVar:
		headerStr = fmt.Sprintf("    %-50s %-20s %-10s %s", "ZARF VARIABLE", "DEFAULT", "TYPE", "LOCATION")
	case v.IsUDSOverrideValue:
		headerStr = fmt.Sprintf("    %-50s %-20s %s", "UDS OVERRIDE PATH", "VALUE", "ORIGINAL DEFAULT")
	case v.IsUDSOverrideVar:
		headerStr = fmt.Sprintf("    %-50s %-30s %s", "UDS VARIABLE", "PATH", "DESCRIPTION")
	default:
		headerStr = fmt.Sprintf("    %-50s %-20s %-10s %s", "PATH", "DEFAULT", "TYPE", "LOCATION")
	}
	return tableHeaderStyle.Render(headerStr)
}
