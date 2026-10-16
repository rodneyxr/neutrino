package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func scopedWorkspaceModel() Model {
	usage := introspect.PackageValueUsage{Path: ".settings", TargetPath: ".", ComponentName: "server", ChartName: "app"}
	return NewModel(introspect.InspectionReport{
		Bundle:         &introspect.BundleMetadata{Format: "next", Name: "demo", Variables: map[string]interface{}{"cluster": "dev"}, Packages: []introspect.BundlePackageMetadata{{ID: "api"}, {ID: "api.extra"}, {ID: "no-chart"}}},
		ComponentOrder: []string{"api/server/app", "api.extra/server/app"},
		Components: map[string]*introspect.IntrospectResult{
			"api/server/app":       {ChartName: "app", Values: []introspect.ValueUsage{{Path: "replicas", DefaultValue: 1}, {Path: "image.tag", DefaultValue: "stable"}}, PackageValueMappings: []introspect.PackageValueUsage{usage}},
			"api.extra/server/app": {ChartName: "app", Values: []introspect.ValueUsage{{Path: "replicas", DefaultValue: 9}}},
		},
		PackageValues: []introspect.PackageValues{
			{PackageName: "api", Values: map[string]interface{}{"settings": map[string]interface{}{"replicas": 2}}, Files: []string{"defaults.yaml", "operator.yaml"}, Usages: []introspect.PackageValueUsage{usage}, Sources: []introspect.ValueSource{
				{ID: "defaults", Name: "defaults.yaml", Status: introspect.ValueResolved, Values: map[string]interface{}{"settings": map[string]interface{}{"replicas": 1}}},
				{ID: "operator", Name: "operator.yaml", Order: 1, Status: introspect.ValueResolved, Values: map[string]interface{}{"settings": map[string]interface{}{"replicas": 2}}},
			}},
			{PackageName: "no-chart", Values: map[string]interface{}{"enabled": false}},
		},
	})
}

func TestHierarchyScopesSeparateSiblingPackagesAndKeepChartlessInputs(t *testing.T) {
	m := scopedWorkspaceModel()
	m.currentChart = "package:api"
	m.allCategories = true
	m.updateFilteredValues()
	if len(m.filteredValues) == 0 {
		t.Fatal("package scope lost all configuration")
	}
	foundHelm, foundInput := false, false
	for _, v := range m.filteredValues {
		if v.Component == "api.extra/server/app" || v.IsBundleVariable {
			t.Fatalf("package scope leaked another owner: %+v", v)
		}
		foundHelm = foundHelm || v.Component == "api/server/app"
		foundInput = foundInput || v.IsPackageValue && v.PackageName == "api"
	}
	if !foundHelm || !foundInput {
		t.Fatal("package scope must combine its package inputs and charts")
	}
	m.currentChart = "package:no-chart"
	m.updateFilteredValues()
	if len(m.filteredValues) != 1 || !m.filteredValues[0].IsPackageValue ||
		m.filteredValues[0].PackageName != "no-chart" || m.filteredValues[0].Path != "enabled" ||
		!m.filteredValues[0].ValuePresent || m.filteredValues[0].DefaultValue != false {
		t.Fatalf("chartless package should retain its configured input without an empty file declaration: %+v", m.filteredValues)
	}
	found := false
	for _, scope := range m.scopes {
		if scope.key == "package:no-chart" {
			found = true
		}
	}
	if !found {
		t.Fatal("chartless package is missing from scope picker")
	}
	m.currentChart = "api/server/app"
	m.updateFilteredValues()
	for _, v := range m.filteredValues {
		if v.Component == "api.extra/server/app" {
			t.Fatal("chart selection used substring ownership")
		}
	}
}

func TestScopeKeepsPackagesWithoutInventingEmptyFileSettings(t *testing.T) {
	m := NewModel(introspect.InspectionReport{Bundle: &introspect.BundleMetadata{
		Format: "next", Packages: []introspect.BundlePackageMetadata{
			{ID: "metadata-only", Source: "oci://registry.example/metadata-only"},
			{ID: "configured", ValuesFiles: []string{"configured.yaml"}},
		},
	}})
	for _, name := range []string{"metadata-only", "configured"} {
		if _, found := m.workspaceScope("package:" + name); !found {
			t.Fatalf("package %q was removed from Scope", name)
		}
	}
	if len(m.filteredValues) != 1 || m.filteredValues[0].Component != "UDS Next bundle" ||
		m.filteredValues[0].PackageName != "configured" ||
		!reflect.DeepEqual(m.filteredValues[0].ValueFiles, []string{"configured.yaml"}) {
		t.Fatalf("Settings should contain only the actual bundle file declaration: %+v", m.filteredValues)
	}
	m.currentChart = "package:metadata-only"
	m.updateFilteredValues()
	if len(m.filteredValues) != 0 {
		t.Fatalf("metadata-only scope invented a setting: %+v", m.filteredValues)
	}
}

func TestScopePickerKeyboardSearchAndCancelPreservesScope(t *testing.T) {
	m := scopedWorkspaceModel()
	updated, _ := m.Update(key("f"))
	m = updated.(Model)
	for _, r := range "no-chart" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.currentChart != "package:no-chart" || m.inputMode != InputModeNormal {
		t.Fatalf("Enter failed to select searched package: %q", m.currentChart)
	}
	updated, _ = m.Update(key("f"))
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.currentChart != "package:no-chart" {
		t.Fatal("cancel changed the selected scope")
	}
}

func TestAllCategorySearchCombinesOwnerAndChangeEvidence(t *testing.T) {
	m := scopedWorkspaceModel()
	m.beginFilter()
	m.filterInput.SetValue("pkg:api status:changed replicas")
	m.updateFilteredValues()
	if !m.allCategories || len(m.filteredValues) == 0 {
		t.Fatal("cross-category search did not find changed input")
	}
	packageInput, helm := false, false
	for _, v := range m.filteredValues {
		if v.Component == "api.extra/server/app" {
			t.Fatal("unrelated package matched changed query")
		}
		if !m.valueChanged(v) {
			t.Fatalf("unchanged item leaked into changed filter: %+v", v)
		}
		packageInput = packageInput || v.IsPackageValue
		helm = helm || v.Component == "api/server/app"
	}
	if !packageInput || !helm {
		t.Fatalf("search must connect package input and Helm destination: %+v", m.filteredValues)
	}
	m.filterInput.SetValue("kind:helm image")
	m.updateFilteredValues()
	if len(m.filteredValues) != 1 || m.filteredValues[0].Path != "image.tag" {
		t.Fatalf("kind + path search mismatch: %+v", m.filteredValues)
	}
}

func TestWorkspaceLayoutsKeepFooterAndControlsWithinTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 32}, {160, 40}} {
		m := scopedWorkspaceModel()
		m.windowWidth, m.windowHeight = size[0], size[1]
		for _, mode := range []string{"values", "overview", "scope", "details", "help", "commands"} {
			m.showOverview = false
			m.showHelp = false
			m.inputMode = InputModeNormal
			m.focusPane = FocusValues
			m.statusMessage = ""
			switch mode {
			case "overview":
				m.showOverview = true
			case "scope":
				m.beginScopePicker()
			case "details":
				m.focusPane = FocusInspector
			case "help":
				m.showHelp = true
			case "commands":
				m.beginCommandPalette()
			}
			view := m.View()
			if lipgloss.Width(view) != size[0] || lipgloss.Height(view) != size[1] {
				t.Fatalf("%s at %v: got %dx%d", mode, size, lipgloss.Width(view), lipgloss.Height(view))
			}
			if mode == "values" || mode == "overview" || mode == "details" {
				lines := strings.Split(ansi.Strip(view), "\n")
				if !strings.Contains(lines[len(lines)-1], "help") {
					t.Fatalf("%s at %v lost persistent footer: %q", mode, size, lines[len(lines)-1])
				}
				if size[0] == 40 {
					for _, status := range []string{"", "Updated the selected scope and retained the current configuration query"} {
						m.statusMessage = status
						compact := assertWorkspaceSize(t, m, size[0], size[1])
						footer := strings.Split(compact, "\n")[size[1]-1]
						for _, control := range []string{"esc back", "? help", "q quit"} {
							if !strings.Contains(footer, control) {
								t.Fatalf("%s at %v with status %q lost %q: %q", mode, size, status, control, footer)
							}
						}
					}
				}
			}
		}
	}
}

func TestTerminalDataCannotInjectCommandsOrRewriteRows(t *testing.T) {
	malicious := "safe\x1b]52;c;payload\a\x1b[2J\rspoof\tname\x00"
	cell := truncateCell(malicious, 80)
	if strings.ContainsAny(cell, "\x1b\a\r\t\x00") {
		t.Fatalf("unsafe cell: %q", cell)
	}
	if got := wrapCell("first\n"+malicious, 80); strings.ContainsAny(got, "\x1b\a\r\t\x00") || !strings.Contains(got, "first\n") {
		t.Fatalf("unsafe multiline: %q", got)
	}
}

func TestSourceFilterFindsHelmTemplateLocations(t *testing.T) {
	m := scopedWorkspaceModel()
	m.components["api/server/app"].Values[0].Locations = []introspect.ValueUsageLocation{{File: "templates/deployment.yaml", Line: 12}}
	m.beginFilter()
	m.filterInput.SetValue("source:deployment.yaml")
	m.updateFilteredValues()
	if len(m.filteredValues) != 1 || m.filteredValues[0].Path != "replicas" {
		t.Fatalf("source search failed to use Helm locations: %+v", m.filteredValues)
	}
}

func TestStandaloneOverviewShowsOnlyObservedConfigurationOwners(t *testing.T) {
	m := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"": {ChartName: "app", Values: []introspect.ValueUsage{{Path: "enabled"}}}}, ComponentOrder: []string{""}})
	m.showOverview = true
	m.windowWidth, m.windowHeight = 120, 32
	view := m.View()
	if strings.Contains(view, "Bundle .vars") || strings.Contains(view, "Zarf Package .Values") || !strings.Contains(view, "Chart defaults") {
		t.Fatalf("standalone overview invented configuration owners:\n%s", view)
	}
}

func TestCategoryNavigationRestoresSelection(t *testing.T) {
	m := scopedWorkspaceModel()
	m.selectNextCategory(5)
	m.cursor = 1
	path := m.filteredValues[m.cursor].Path
	m.selectNextCategory(0)
	m.selectNextCategory(5)
	if m.cursor != 1 || m.filteredValues[m.cursor].Path != path {
		t.Fatal("switching categories lost selected chart value")
	}
}

func TestTinyPaletteAndOverviewKeepBordersAndActionsVisible(t *testing.T) {
	for _, size := range [][2]int{{30, 10}, {40, 12}, {80, 24}} {
		m := scopedWorkspaceModel()
		m.windowWidth, m.windowHeight = size[0], size[1]
		m.beginCommandPalette()
		for _, cursor := range []int{0, len(commands) - 1} {
			m.commandCursor = cursor
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "esc") || !strings.Contains(view, "╚") || !strings.Contains(view, ">") {
				t.Fatalf("palette at%v lost footer, border, or selection:\n%s", size, view)
			}
		}
		m.inputMode = InputModeNormal
		m.showOverview = true
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "╚") {
			t.Fatalf("overview at%v lost bottom border:\n%s", size, view)
		}
	}
}

func TestSmallDetailsInitialTraceActionIsVisible(t *testing.T) {
	m := scopedWorkspaceModel()
	m.bundle.VariableSources = []introspect.ValueSource{{ID: "defaults", Name: "source.hcl", Kind: "bundle-defaults", Status: introspect.ValueResolved, MaterializedFile: "source.hcl", Values: map[string]interface{}{"cluster": "dev"}}}
	m.windowWidth, m.windowHeight = 40, 12
	m.selectNextCategory(0)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.focusPane != FocusInspector || len(m.detailItems) == 0 {
		t.Fatal("fixture failed to open an actionable trace")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "> ") || !strings.Contains(view, "cluster") {
		t.Fatalf("initial trace action and value identity must remain visible:\n%s", view)
	}
}

func TestScopeCancelPreservesInspectorRouteAndQueryAcrossResize(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		widths []int
	}{{"overlay", []int{80, 40, 160}}, {"tree", []int{160, 80, 40, 160}}} {
		t.Run(scenario.name, func(t *testing.T) {
			m := persistentWorkspaceModel(t)
			resizeWorkspace(&m, scenario.widths[0], 24)
			m.filterInput.SetValue("kind:helm")
			m.updateFilteredValues()
			pressWorkspaceKey(&m, "j")
			pressWorkspaceKey(&m, "enter")
			openInspectorTestPanel(t, &m, detailSources)
			m.detailCursor = 3
			pressWorkspaceKey(&m, "enter")
			source, history := m.detailSource, append([]detailPanelReturn(nil), m.detailPanelHistory...)
			selected, cursor := m.inspectorSelection, m.cursor
			pressWorkspaceKey(&m, "f")
			if scenario.name == "overlay" {
				for _, r := range "missing-chart" {
					updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
					m = updated.(Model)
				}
			} else {
				pressWorkspaceKey(&m, "j")
				pressWorkspaceKey(&m, "f") // Reentering must retain the original Inspector caller.
			}
			key, mode := m.scopeTreeKey, m.inputMode
			for _, width := range scenario.widths[1:] {
				resizeWorkspace(&m, width, 24)
				assertWorkspaceSize(t, m, width, 24)
				if m.inputMode != mode || m.scopeTreeKey != key {
					t.Fatal("resizing changed active Scope navigation")
				}
			}
			pressWorkspaceKey(&m, "esc")
			if m.inputMode != InputModeNormal || m.focusPane != FocusInspector || m.detailPanel != detailSourceFile ||
				m.detailSource != source || len(history) != 2 || !reflect.DeepEqual(m.detailPanelHistory, history) ||
				m.filterInput.Value() != "kind:helm" || m.currentChart != "All Charts" ||
				m.cursor != cursor || m.inspectorSelection != selected || m.scopeTreeKey != key ||
				m.workspaceGeometry(m.windowWidth).scope != 0 {
				t.Fatal("canceling Scope changed its caller, Inspector route, selected setting, query, or scope")
			}
		})
	}
}

func TestScopeCancelPreservesMappingAcrossResize(t *testing.T) {
	m := scopedWorkspaceModel()
	resizeWorkspace(&m, 160, 24)
	m.selectNextCategory(4)
	pressWorkspaceKey(&m, "enter")
	openInspectorTestMapping(t, &m)
	table, selected := m.mapping, m.inspectorSelection
	cursor, offset := table.cursor, table.offset
	pressWorkspaceKey(&m, "shift+tab")
	if m.focusPane != FocusComponents || m.scopeReturnFocus != FocusMapping {
		t.Fatal("mapping table did not open Scope with its return focus")
	}
	pressWorkspaceKey(&m, "j")
	key := m.scopeTreeKey
	for _, width := range []int{80, 40, 160} {
		resizeWorkspace(&m, width, 24)
		assertWorkspaceSize(t, m, width, 24)
		if m.focusPane != FocusComponents || m.scopeTreeKey != key || m.mapping != table {
			t.Fatal("resizing Scope changed its active navigation or mapping table")
		}
	}
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusMapping || m.mapping != table || table.cursor != cursor || table.offset != offset ||
		m.inspectorSelection != selected || m.currentChart != "All Charts" || m.scopeTreeKey != key ||
		m.workspaceGeometry(m.windowWidth).scope != 0 {
		t.Fatal("canceling Scope did not restore the same mapping selection")
	}
}

func TestApplyingScopeClosesMappingAndRetainsQuery(t *testing.T) {
	m := scopedWorkspaceModel()
	resizeWorkspace(&m, 80, 24)
	m.filterInput.SetValue("pkg:api")
	m.updateFilteredValues()
	m.selectNextCategory(4)
	if len(m.filteredValues) != 1 || !m.filteredValues[0].IsPackageMapping {
		t.Fatalf("fixture did not select a mapping declaration: %+v", m.filteredValues)
	}
	pressWorkspaceKey(&m, "enter")
	if m.mapping != nil || m.focusPane != FocusInspector {
		t.Fatal("Settings Enter should inspect the declaration before opening its mapping")
	}
	openInspectorTestMapping(t, &m)
	table := m.mapping
	pressWorkspaceKey(&m, "tab")
	if m.focusPane != FocusInspector || m.mapping != table {
		t.Fatal("mapping table could not move focus to its live inspector")
	}
	pressWorkspaceKey(&m, "shift+tab")
	if m.focusPane != FocusMapping || m.mapping != table {
		t.Fatal("mapping Inspector did not return focus to the same table")
	}
	pressWorkspaceKey(&m, "f")
	if m.inputMode != InputModeScopeSelect {
		t.Fatal("scope selection is unreachable from a mapping table")
	}
	for _, r := range "no-chart" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	pressWorkspaceKey(&m, "enter")
	if m.currentChart != "package:no-chart" || m.mapping != nil || m.mappingRowDetail || m.detailRef != nil ||
		m.focusPane != FocusValues || m.filterInput.Value() != "pkg:api" || m.detailPanel != detailSummary {
		t.Fatal("applying a new scope did not clear mapping routes while retaining the query")
	}
}
