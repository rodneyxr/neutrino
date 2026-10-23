package tui

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
)

// InputMode defines the input state
type InputMode int

const (
	InputModeNormal InputMode = iota
	InputModeFilter
	InputModeChartSelect
	InputModeCommand
	InputModeScopeSelect
)

// DetailItemKind distinguishes navigable items in the detail view.
type DetailItemKind int

const (
	DetailItemLocation DetailItemKind = iota
	DetailItemCrossRef
	DetailItemMapping
	DetailItemFile
	DetailItemTrace
	DetailItemPanel
	DetailItemSource
	DetailItemPreview
)

// DetailItem represents a single navigable row in the detail view.
type DetailItem struct {
	traceID           string
	Kind              DetailItemKind
	Location          introspect.ValueUsageLocation // valid when Kind == DetailItemLocation
	sourceComponent   string                        // owning chart when a package variable links to chart usage
	sourceRole        string                        // display role kept separately from shortened coordinates
	sourceDisplayPath string                        // display only; File/Location retain exact source identity
	relatedValue      *DisplayItem                  // target independent of workspace filtering
	Label             string                        // rendered label for cross-ref items
	Mapping           introspect.PackageValueUsage  // valid when Kind == DetailItemMapping
	packageMapping    *valueMappingDetail
	mappingRef        *valueMapping
	File              string // resolved path when Kind == DetailItemFile
	panel             detailPanel
	preview           *inspectorValuePreview
}

// inspectorValuePreview contains the already-masked input for one explicit
// comparison action, independent of the selected setting or chart baseline.
type inspectorValuePreview struct {
	title string
	value interface{}
}

type DisplayItem struct {
	introspect.ValueUsage
	Component          string
	IsZarfVar          bool
	IsPkgVar           bool
	PkgVarDesc         string
	ZarfReferences     []zarfVariableReference
	ZarfLocationCount  int
	ZarfFirstLocation  introspect.ValueUsageLocation
	IsEmptyState       bool
	IsUDSOverrideValue bool
	IsUDSOverrideVar   bool
	IsSummaryItem      bool        // true for top-level UDS summary items
	OverrideValue      interface{} // The value being set (for override values)
	OriginalDefault    interface{} // The original chart default being overridden
	UDSVarPath         string      // The Helm path for UDS override variables
	UDSVarDescription  string      // Description for UDS override variables
	SummaryComponent   string      // source component for UDS summary items
	PackageName        string
	MappingComponent   string
	MappingChart       string
	IsPackageValue     bool
	IsPackageMapping   bool
	IsBundleVariable   bool
	ValuePresent       bool
	ValueSchema        interface{}
	ValueFiles         []string
	ValueFileSources   []string
	ValueSource        string
	ValueTarget        string
	ExcludedPaths      []string
	Runtime            bool
	Routes             []packageRoute
	MappingStatus      string
	mappingRef         *valueMapping
	authoring          *valueAuthoring
	settingLabel       string
	settingParent      string
	settingDepth       int
	settingChildren    int
	settingContext     bool
}

type zarfVariableReference struct {
	component string
	locations []introspect.ValueUsageLocation
}

type Model struct {
	// Data
	report               introspect.InspectionReport
	scopes               []workspaceScope
	scopeCursor          int
	showOverview         bool
	overviewOffset       int
	allCategories        bool
	allValues            []DisplayItem
	previousFilter       string
	components           map[string]*introspect.IntrospectResult
	chartNames           []string
	currentChart         string
	filteredValues       []DisplayItem
	udsOverrideValues    []introspect.UDSOverrideValueWithContext
	udsOverrideVariables []introspect.UDSOverrideVariableWithContext
	valuePolicy          *packageValuesPolicy
	bundle               *introspect.BundleMetadata
	nextRail             bool
	category             int
	categoryCounts       [7]int
	categoryRows         [7][]DisplayItem
	categoryCursors      [7]int
	categoryOffsets      [7]int

	// State
	inputMode          InputMode
	focusPane          FocusPane
	inspectorSelection string
	scopeExpanded      map[string]bool
	scopeTreeKey       string
	scopeReturnFocus   FocusPane
	cursor             int
	componentCursor    int
	detailCursor       int
	detailScrollOffset int
	detailBundleMode   bool
	detailSchemaShown  bool
	detailItems        []DetailItem
	viewportOffset     int
	mapping            *mappingScreen
	mappingRowDetail   bool
	detailRef          *DisplayItem
	traceHistory       []traceReturn
	detailPanel        detailPanel
	detailPanelHistory []detailPanelReturn
	detailSource       *DetailItem
	pathScroll         int
	pathScrollPause    int
	valueScroll        int
	valueScrollPause   int
	compScroll         int
	compScrollPause    int
	windowHeight       int
	windowWidth        int
	showZarfSummary    bool
	showUDSSummary     bool
	showHelp           bool
	helpOffset         int
	statusMessage      string
	statusExpiresAt    time.Time

	// Inputs
	filterInput   textinput.Model
	chartInput    textinput.Model
	commandInput  textinput.Model
	commandCursor int
	previousChart string
	chartQuery    string
	pkgVariables  []v1alpha1.InteractiveVariable
}

func NewModel(report introspect.InspectionReport) Model {

	components := report.Components
	pkgVariables := report.Variables

	// Keep package/chart identity order deterministic even when a report omits
	// optional ComponentOrder entries.
	charts := append([]string{"All Charts"}, orderedScopeCharts(report)...)

	currentChart := "All Charts"

	// Initialize inputs
	fi := textinput.New()
	fi.Placeholder = "path  pkg:api  status:unresolved"
	fi.Prompt = "/ "
	fi.ShowSuggestions = true

	ci := textinput.New()
	ci.Placeholder = "Select component..."
	ci.Prompt = "f "
	ci.ShowSuggestions = true

	commands := textinput.New()
	commands.Placeholder = "Type a command..."
	commands.Prompt = ": "

	m := Model{
		report:               report,
		scopes:               buildWorkspaceScopes(report),
		components:           components,
		chartNames:           charts,
		currentChart:         currentChart,
		inputMode:            InputModeNormal,
		focusPane:            FocusValues,
		filterInput:          fi,
		chartInput:           ci,
		commandInput:         commands,
		pkgVariables:         pkgVariables,
		udsOverrideValues:    report.UDSOverrideValues,
		udsOverrideVariables: report.UDSOverrideVariables,
		valuePolicy:          newPackageValuesPolicy(report),
		bundle:               report.Bundle,
		nextRail:             report.Bundle != nil && report.Bundle.Format == "next",
		showZarfSummary:      len(pkgVariables) > 0 || report.Bundle != nil && report.Bundle.Format == "next",
	}

	m.updateFilteredValues()
	m.selectNextCategory(m.initialSettingCategory())
	m.syncScopeCursor()
	m.syncInspectorSelection()
	return m
}

func (m *Model) updateFilteredValues() {
	m.resetSelectedRowScroll()
	var newValues []DisplayItem
	// Filter the completed catalog so every row type shares the same query semantics.
	filter := ""

	// Package Values are package-level inputs, not chart defaults. Keep them
	// visible even when a package contains no charts.
	if m.currentChart == "All Charts" || m.currentChart == "bundle:inputs" || strings.HasPrefix(m.currentChart, "package:") {
		for _, entry := range m.valuePolicy.catalog().packages {
			item := packageValueItem(entry, "Zarf Package Values")
			if matchesValueFilter(item, filter) {
				newValues = append(newValues, item)
			}
		}
		if m.bundle != nil {
			for _, item := range m.bundleVariableItems() {
				if matchesValueFilter(item, filter) {
					newValues = append(newValues, item)
				}
			}
			for _, pkg := range m.bundle.Packages {
				// Packages without values files remain in Scope; they are not
				// configuration inputs in the Settings catalog.
				if len(pkg.ValuesFiles) == 0 {
					continue
				}
				item := DisplayItem{ValueUsage: introspect.ValueUsage{Path: pkg.ID},
					Component: "UDS Next bundle", PackageName: pkg.ID,
					IsPackageValue: true, ValueFiles: pkg.ValuesFiles, ValueSource: pkg.Source}
				if matchesValueFilter(item, filter) {
					newValues = append(newValues, item)
				}
			}
			if len(m.bundle.Variables) == 0 && len(m.bundle.Packages) == 0 {
				item := DisplayItem{ValueUsage: introspect.ValueUsage{Path: m.bundle.Name},
					Component: "UDS Next bundle", IsBundleVariable: true}
				if matchesValueFilter(item, filter) {
					newValues = append(newValues, item)
				}
			}
		}
	}

	// Add package variables at the top if the Zarf summary toggle is on
	if m.showZarfSummary {
		for _, pv := range m.pkgVariables {
			item := DisplayItem{
				ValueUsage: introspect.ValueUsage{
					Path:         pv.Name,
					DefaultValue: pv.Default,
				},
				Component:   "Zarf Package Variables",
				PackageName: m.nextPackageFor(pv.Name, "."),
				IsPkgVar:    true,
				PkgVarDesc:  pv.Description,
			}
			if filter == "" || strings.Contains(strings.ToLower(pv.Name), strings.ToLower(filter)) {
				newValues = append(newValues, item)
			}
		}
	}

	// Keep overrides whose destination row is unavailable. The summary toggle
	// controls only extra copies of overrides already shown with their chart.
	for _, ov := range m.udsOverrideValues {
		destComponent := fmt.Sprintf("%s/%s/%s", ov.PackageName, ov.ComponentName, ov.ChartName)
		chartHasOverride := false
		if chart := m.components[destComponent]; chart != nil {
			for _, chartOverride := range chart.UDSOverrideValues {
				if chartOverride.Path == ov.Path {
					chartHasOverride = true
					break
				}
			}
		}
		if chartHasOverride && !m.showUDSSummary {
			continue
		}
		if filter == "" || strings.Contains(strings.ToLower(ov.Path), strings.ToLower(filter)) {
			newValues = append(newValues, DisplayItem{
				ValueUsage: introspect.ValueUsage{
					Path:         ov.Path,
					DefaultValue: ov.OriginalDefault,
				},
				Component:          "UDS Bundle Overrides",
				PackageName:        ov.PackageName,
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
		chartHasOverride := false
		if chart := m.components[destComponent]; chart != nil {
			for _, chartOverride := range chart.UDSOverrideVariables {
				if chartOverride.Name == ov.Name && chartOverride.Path == ov.Path {
					chartHasOverride = true
					break
				}
			}
		}
		if chartHasOverride && !m.showUDSSummary {
			continue
		}
		searchStr := strings.ToLower(ov.Name + " " + ov.Path)
		if filter == "" || strings.Contains(searchStr, strings.ToLower(filter)) {
			newValues = append(newValues, DisplayItem{
				ValueUsage: introspect.ValueUsage{
					Path:         ov.Name,
					DefaultValue: ov.Default,
				},
				Component:         "UDS Bundle Overrides",
				PackageName:       ov.PackageName,
				IsUDSOverrideVar:  true,
				IsSummaryItem:     true,
				UDSVarPath:        ov.Path,
				UDSVarDescription: ov.Description,
				OriginalDefault:   ov.OriginalDefault,
				SummaryComponent:  destComponent,
			})
		}
	}

	// Iterate valid chart names to maintain order and get keys
	for _, name := range m.chartNames {
		if name == "All Charts" {
			continue
		}

		// If viewing a specific chart, skip others
		if !m.chartInScope(name) {
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

				newValues = append(newValues, item)
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

			item := DisplayItem{
				ValueUsage:  v,
				Component:   name,
				PackageName: m.nextPackageFor(name, "/"),
				IsZarfVar:   true,
			}
			if filter == "" || (m.nextRail && matchesValueFilter(item, filter)) ||
				(!m.nextRail && strings.Contains(strings.ToLower(v.Path), strings.ToLower(filter))) {
				chartHasItems = true
				newValues = append(newValues, item)
			}
		}

		if m.currentChart != "All Charts" && !strings.HasPrefix(m.currentChart, "package:") {
			for _, entry := range m.valuePolicy.catalog().charts[name] {
				item := packageValueItem(entry, name)
				if matchesValueFilter(item, filter) {
					chartHasItems = true
					newValues = append(newValues, item)
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
				newValues = append(newValues, DisplayItem{
					ValueUsage: introspect.ValueUsage{
						Path:         ov.Name,
						DefaultValue: ov.Default,
					},
					Component:         name,
					IsUDSOverrideVar:  true,
					UDSVarPath:        ov.Path,
					UDSVarDescription: ov.Description,
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
		priority := func(item DisplayItem) int {
			switch {
			case item.IsPkgVar:
				return 0
			case item.Component == "UDS Next bundle":
				return 1
			case item.Component == "Zarf Package Values":
				return 2
			case item.IsSummaryItem:
				return 3
			default:
				return 4
			}
		}
		if priority(newValues[i]) != priority(newValues[j]) {
			return priority(newValues[i]) < priority(newValues[j])
		}
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

		if priority(newValues[i]) < 4 {
			return false
		}

		if newValues[i].IsPackageMapping != newValues[j].IsPackageMapping {
			return newValues[i].IsPackageMapping
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

		return newValues[i].Path < newValues[j].Path
	})

	if m.nextRail {
		newValues = m.consolidateNextZarfVariables(newValues, filter)
	}
	newValues = m.projectSettingsCatalog(newValues)
	for i := range newValues {
		newValues[i] = m.filteredReferences(newValues[i])
	}
	newValues = m.orderSettingsLayers(m.filterSettingsCatalog(newValues))
	m.allValues = newValues
	for i := range m.categoryCounts {
		m.categoryCounts[i] = 0
	}
	for _, item := range newValues {
		m.categoryCounts[nextCategory(item)]++
	}
	start := 0
	for i, count := range m.categoryCounts {
		m.categoryRows[i] = newValues[start : start+count]
		start += count
	}
	m.filteredValues = m.categoryRows[m.category]
	if m.allCategories {
		m.filteredValues = m.allValues
	}
	newValues = m.filteredValues

	// Set Autocomplete suggestions
	if m.inputMode == InputModeFilter {
		input := m.filterInput.Value()
		var suggestions []string
		if input != "" {
			for _, v := range newValues {
				if strings.HasPrefix(strings.ToLower(v.Path), strings.ToLower(input)) {
					// Distinct items may share a path; suggest each path only once.
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
		m.chartInput.SetSuggestions(suggestions)
	}

	// Reset cursor if out of bounds
	if m.cursor >= len(m.filteredValues) {
		m.cursor = 0
	}
	m.cursor = clamp(m.cursor, 0, len(m.filteredValues)-1)
	m.syncInspectorSelection()
}

func packageValueItem(entry packageValueEntry, component string) DisplayItem {
	if entry.mapping != nil {
		usage := entry.mapping.usage
		packageName := ""
		if entry.pkg != nil {
			packageName = entry.pkg.PackageName
		}
		return DisplayItem{
			ValueUsage: introspect.ValueUsage{Path: usage.Path, Locations: usage.Locations},
			Component:  component, PackageName: packageName,
			MappingComponent: usage.ComponentName, MappingChart: usage.ChartName,
			IsPackageMapping: true, ValueSource: usage.Path,
			ValueTarget: usage.TargetPath, ExcludedPaths: usage.ExcludePaths, Runtime: usage.Runtime,
			mappingRef: entry.mapping,
		}
	}
	var schema interface{}
	if len(entry.pkg.Schema) > 0 {
		schema = entry.pkg.Schema
	}
	return DisplayItem{
		ValueUsage: introspect.ValueUsage{Path: entry.path, DefaultValue: entry.value, DefaultPresent: entry.present, Schema: entry.schema, SchemaRequired: entry.schemaRequired},
		Component:  component, PackageName: entry.pkg.PackageName,
		IsPackageValue: true, ValuePresent: entry.present,
		ValueFiles: entry.pkg.Files, ValueFileSources: entry.pkg.FileSources,
		ValueSchema: schema, Routes: entry.routes, MappingStatus: entry.status,
		authoring: entry.authoring,
	}
}

func matchesValueFilter(item DisplayItem, filter string) bool {
	if filter == "" {
		return true
	}
	haystack := strings.Join([]string{item.Path, item.PackageName, item.Component,
		item.ValueSource, item.ValueTarget, strings.Join(item.ValueFiles, " "),
		strings.Join(item.ExcludedPaths, " ")}, " ")
	for _, route := range item.Routes {
		haystack += " " + route.Usage.ComponentName + " " + route.Usage.ChartName +
			" " + route.Usage.TargetPath + " " + route.Target + " " + strings.Join(route.Usage.ExcludePaths, " ")
	}
	for _, ref := range item.ZarfReferences {
		haystack += " " + ref.component
		for _, location := range ref.locations {
			haystack += " " + location.File
		}
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(filter))
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickPathScroll())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.showHelp {
			switch msg.String() {
			case "?", "esc":
				m.showHelp = false
			case "j", "down":
				m.helpOffset++
			case "k", "up":
				m.helpOffset = max(0, m.helpOffset-1)
			case "pgdown":
				m.helpOffset += max(1, m.windowHeight-8)
			case "pgup":
				m.helpOffset = max(0, m.helpOffset-max(1, m.windowHeight-8))
			case "g":
				m.helpOffset = 0
			case "G":
				m.helpOffset = 100
			case "q":
				return m, tea.Quit
			}
			return m, nil
		}
		previousFocus := m.focusPane
		var next tea.Model
		var cmd tea.Cmd
		switch m.inputMode {
		case InputModeFilter:
			next, cmd = m.handleFilterKey(msg)
		case InputModeChartSelect:
			next, cmd = m.handleComponentKey(msg)
		case InputModeCommand:
			next, cmd = m.handleCommandKey(msg)
		case InputModeScopeSelect:
			next, cmd = m.handleScopeKey(msg)
		default:
			next, cmd = m.handleNormalKey(msg)
		}
		updated := next.(Model)
		if updated.focusPane != previousFocus {
			updated.resetSelectedRowScroll()
			updated.refreshWorkspaceGeometry()
		}
		return updated, cmd
	case pathScrollTickMsg:
		if m.statusMessage != "" && !time.Time(msg).Before(m.statusExpiresAt) {
			m.statusMessage = ""
		}
		m.stepAutoScroll()
		return m, tickPathScroll()
	case tea.WindowSizeMsg:
		m.windowHeight = msg.Height
		m.windowWidth = msg.Width
		m.resetSelectedRowScroll()
		m.compScroll = 0
		m.compScrollPause = 2
		m.refreshWorkspaceGeometry()
	}
	return m, nil
}

// ensureCursorVisible keeps the selected value and its group heading visible.
func (m *Model) ensureCursorVisible() {
	if len(m.filteredValues) == 0 {
		m.cursor = 0
		m.viewportOffset = 0
		return
	}
	m.cursor = clamp(m.cursor, 0, len(m.filteredValues)-1)
	visibleLines := max(2, m.windowHeight-10)
	if m.cursor < m.viewportOffset {
		m.viewportOffset = m.cursor
	}
	for m.viewportOffset < m.cursor && m.valueLineCount(m.viewportOffset, m.cursor) > visibleLines {
		m.viewportOffset++
	}
	m.viewportOffset = clamp(m.viewportOffset, 0, len(m.filteredValues)-1)
}

func (m Model) View() string {
	return fitWorkspace(m.workspaceView(), m.windowWidth, m.windowHeight)
}

// findOverridesForPath resolves related values from the chart, not the filtered workspace.
func (m Model) findOverridesForPath(component, path string) []DetailItem {
	var items []DetailItem
	res := m.components[component]
	if res == nil {
		return items
	}
	for _, ov := range res.UDSOverrideValues {
		if ov.Path == path {
			items = append(items, DetailItem{
				Kind:  DetailItemCrossRef,
				Label: fmt.Sprintf("→ UDS Override Value: %s = %s", ov.Path, formatDefaultValue(m.displayValue(DisplayItem{ValueUsage: introspect.ValueUsage{Path: ov.Path}, Component: component}, ov.Value))),
				relatedValue: &DisplayItem{
					ValueUsage: introspect.ValueUsage{Path: ov.Path, DefaultValue: ov.OriginalDefault},
					Component:  component, IsUDSOverrideValue: true,
					OverrideValue: ov.Value, OriginalDefault: ov.OriginalDefault,
				},
			})
		}
	}
	for _, ov := range res.UDSOverrideVariables {
		if ov.Path == path {
			items = append(items, DetailItem{
				Kind:  DetailItemCrossRef,
				Label: fmt.Sprintf("→ UDS Variable: %s → %s", ov.Name, ov.Path),
				relatedValue: &DisplayItem{
					ValueUsage: introspect.ValueUsage{Path: ov.Name, DefaultValue: ov.Default},
					Component:  component, IsUDSOverrideVar: true, UDSVarPath: ov.Path,
					UDSVarDescription: ov.Description, OriginalDefault: ov.OriginalDefault,
				},
			})
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
			items = append(items, DetailItem{
				Kind:         DetailItemCrossRef,
				Label:        fmt.Sprintf("→ Helm Value: %s (%d locations)", path, len(v.Locations)),
				relatedValue: &DisplayItem{ValueUsage: v, Component: component},
			})
			break
		}
	}
	return items
}

// buildDetailItems constructs the unified navigable list for the detail view of the current item.
func (m *Model) buildTechnicalDetailItems() {
	v, ok := m.detailValue()
	if !ok {
		m.detailItems = nil
		return
	}
	items := m.traceDetailItems(v)
	if !v.IsZarfVar && !v.IsPkgVar && !v.IsPackageValue && !v.IsPackageMapping && !v.IsBundleVariable && !v.IsUDSOverrideValue && !v.IsUDSOverrideVar {
		details := m.valuePolicy.details(v.Component, v.ValueUsage)
		for i := range details {
			detail := &details[i]
			items = append(items, DetailItem{
				Kind: DetailItemMapping, Mapping: detail.mapping.usage,
				packageMapping: detail, mappingRef: detail.mapping,
			})
		}
	}
	if v.IsPackageValue && v.Component == "Zarf Package Values" {
		for _, route := range v.Routes {
			if route.Usage.TargetPath != "" && route.Usage.ChartName != "" {
				items = append(items, DetailItem{
					Kind: DetailItemMapping, Mapping: route.Usage, mappingRef: route.mapping,
				})
			}
		}
	}
	for _, usage := range m.observedValueUsages(v) {
		for _, location := range usage.Locations {
			items = append(items, DetailItem{Kind: DetailItemLocation, Location: location,
				Label: "Template use", sourceComponent: observedUsageOwner(v, usage)})
		}
	}

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
	} else if len(v.ZarfReferences) > 0 {
		for _, ref := range v.ZarfReferences {
			for _, loc := range ref.locations {
				items = append(items, DetailItem{
					Kind: DetailItemLocation, Location: loc, sourceComponent: ref.component,
				})
			}
		}
	} else if !v.IsPkgVar {
		// Regular Helm value or Zarf var: show locations, then cross-refs to UDS overrides
		for _, loc := range v.Locations {
			items = append(items, DetailItem{
				Kind:     DetailItemLocation,
				Location: loc,
			})
		}
		if !v.IsZarfVar && !v.IsPackageMapping && !v.IsPackageValue && !v.IsBundleVariable {
			items = append(items, m.findOverridesForPath(v.Component, v.Path)...)
		}
	}
	for _, group := range detailFileGroups(v, items) {
		for _, file := range group.files {
			if file.source != "" {
				items = append(items, DetailItem{Kind: DetailItemFile, File: file.source})
			}
		}
	}

	m.detailItems = items
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
	return generateBundleSnippetWithParts(pkgName, componentName, chartName, helmPath, defaultValue)
}

func (m Model) generateBundleSnippetForItem(v DisplayItem) string {
	v.DefaultValue = m.displayValue(v, v.DefaultValue)
	if (m.bundle != nil && m.bundle.Format != "legacy") || v.IsPackageValue || v.IsPackageMapping || v.IsBundleVariable {
		return ""
	}
	pkgName, componentName, chartName := parseComponentKey(v.Component)
	if pkgName == "" {
		if !m.detailBundleMode {
			return ""
		}
		pkgName = "<package-name>"
		if componentName == "" {
			componentName = "<component-name>"
		}
		if chartName == "" {
			chartName = v.Chart
			if chartName == "" {
				if res := m.components[v.Component]; res != nil {
					chartName = res.ChartName
				}
			}
			if chartName == "" {
				chartName = "<chart-name>"
			}
		}
	}
	return generateBundleSnippetWithParts(pkgName, componentName, chartName, v.Path, v.DefaultValue)
}

func generateBundleSnippetWithParts(pkgName, componentName, chartName, helmPath string, defaultValue interface{}) string {
	varName := helmPathToVarName(helmPath)
	defaultStr := formatSnippetDefaultYAML(defaultValue, 16)
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

func cleanWholeNumbers(val interface{}) interface{} {
	switch v := val.(type) {
	case float64:
		if v == math.Trunc(v) && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return int64(v)
		}
		return v
	case float32:
		if float64(v) == math.Trunc(float64(v)) {
			return int64(v)
		}
		return v
	case map[string]interface{}:
		res := make(map[string]interface{}, len(v))
		for k, item := range v {
			res[k] = cleanWholeNumbers(item)
		}
		return res
	case map[interface{}]interface{}:
		res := make(map[interface{}]interface{}, len(v))
		for k, item := range v {
			res[k] = cleanWholeNumbers(item)
		}
		return res
	case []interface{}:
		res := make([]interface{}, len(v))
		for i, item := range v {
			res[i] = cleanWholeNumbers(item)
		}
		return res
	default:
		return val
	}
}

func formatSnippetDefaultYAML(defaultValue interface{}, indent int) string {
	if defaultValue == nil {
		return "null"
	}
	defaultValue = cleanWholeNumbers(defaultValue)
	b, err := yaml.Marshal(defaultValue)
	if err != nil {
		return fmt.Sprintf("%v", defaultValue)
	}
	raw := strings.TrimRight(string(b), "\n")
	if raw == "" || raw == "null" {
		return "null"
	}
	if !strings.Contains(raw, "\n") {
		return raw
	}
	prefix := strings.Repeat(" ", indent)
	lines := strings.Split(raw, "\n")
	var result strings.Builder
	result.WriteByte('\n')
	for i, line := range lines {
		result.WriteString(prefix)
		result.WriteString(line)
		if i < len(lines)-1 {
			result.WriteByte('\n')
		}
	}
	return result.String()
}

func formatYAMLValue(val interface{}) string {
	if val == nil {
		return "-"
	}
	val = cleanWholeNumbers(val)
	b, err := yaml.Marshal(val)
	if err != nil {
		return fmt.Sprintf("%v", val)
	}
	str := strings.TrimRight(string(b), "\n")
	if str == "" || str == "null" {
		return "-"
	}
	return str
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
	val = cleanWholeNumbers(val)
	valBytes, _ := yaml.MarshalWithOptions(val, yaml.Flow(true))
	return strings.TrimSpace(string(valBytes))
}
