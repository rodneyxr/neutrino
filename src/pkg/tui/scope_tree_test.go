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

func scopeTreeKeys(scopes []workspaceScope) []string {
	var keys []string
	for _, scope := range scopes {
		keys = append(keys, scope.key)
	}
	return keys
}

func TestScopeTreeGroupsActualOwnersAndComponents(t *testing.T) {
	report := introspect.InspectionReport{
		Bundle:        &introspect.BundleMetadata{Packages: []introspect.BundlePackageMetadata{{ID: "api"}, {ID: "api.v2"}, {ID: "chartless"}}},
		PackageValues: []introspect.PackageValues{{PackageName: "extra-chartless"}},
		Components: map[string]*introspect.IntrospectResult{
			"api/server/first": {}, "api/worker/task": {}, "api/server/second": {}, "api.v2/server/web": {}, "legacy/server/web": {},
		},
		ComponentOrder: []string{"api/server/first", "api/worker/task", "api/server/second", "api/server/first", "missing/chart"},
	}
	scopes := buildWorkspaceScopes(report)
	want := []string{"All Charts", "bundle:inputs", "package:api", "component:api/server", "api/server/first", "api/server/second",
		"component:api/worker", "api/worker/task", "package:api.v2", "component:api.v2/server", "api.v2/server/web",
		"package:chartless", "package:extra-chartless", "package:legacy", "component:legacy/server", "legacy/server/web"}
	if got := scopeTreeKeys(scopes); !reflect.DeepEqual(got, want) {
		t.Fatalf("scope hierarchy lost owners, contiguous components, or map-only charts:\ngot  %v\nwant %v", got, want)
	}
	m := Model{scopes: scopes, currentChart: "package:api"}
	if !m.chartInScope("api/server/first") || m.chartInScope("api.v2/server/web") || m.chartInScope("api/server/missing") {
		t.Fatal("package scope must use exact inspected membership")
	}
	m.currentChart = "component:api/server"
	if !m.chartInScope("api/server/second") || m.chartInScope("api/worker/task") {
		t.Fatal("component scope crossed component identity")
	}
	if m.itemInScope(DisplayItem{IsSummaryItem: true, SummaryComponent: "api/worker/task"}) || !m.itemInScope(DisplayItem{IsSummaryItem: true, SummaryComponent: "api/server/first"}) {
		t.Fatal("legacy summary override ignored exact target component")
	}
}

func TestScopeTreeDirectPackageAndStandaloneChartIdentities(t *testing.T) {
	report := introspect.InspectionReport{
		PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{"enabled": false}}},
		Components:    map[string]*introspect.IntrospectResult{"core/web": {ChartName: "web"}},
	}
	m := Model{report: report, scopes: buildWorkspaceScopes(report), currentChart: "package:core"}
	if !m.chartInScope("core/web") {
		t.Fatal("direct Zarf package lost its unprefixed component/chart")
	}
	chart, found := m.workspaceScope("core/web")
	if !found || chart.packageName != "core" || chart.parentKey != "component:core" {
		t.Fatalf("direct component named like the package was misclassified: %+v", chart)
	}
	chartless := buildWorkspaceScopes(introspect.InspectionReport{PackageValues: report.PackageValues})
	if got := scopeTreeKeys(chartless); !reflect.DeepEqual(got, []string{"All Charts", "package:core"}) {
		t.Fatalf("chartless direct package missing: %v", got)
	}
	standalone := introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"": {ChartName: "podinfo"}}}
	m = Model{scopes: buildWorkspaceScopes(standalone), currentChart: ""}
	chart, found = m.workspaceScope("")
	if !found || chart.kind != "chart" || chart.label != "podinfo" || chart.packageName != "" || !m.chartInScope("") || m.chartInScope("other") {
		t.Fatalf("standalone empty chart identity was treated as absent: %+v", chart)
	}
	m.syncScopeCursor()
	if m.scopeTreeKey != "" || len(m.visibleScopeTree()) != 2 {
		t.Fatal("standalone chart could not be selected in the tree")
	}
}

func TestScopeTreeCollapsesPackagesAndRevealsActiveAncestors(t *testing.T) {
	m := scopedWorkspaceModel()
	m.scopeCursor = 9 // The overlay's cursor must remain independent.
	m.syncScopeCursor()
	if got := scopeTreeKeys(m.visibleScopeTree()); !reflect.DeepEqual(got, []string{"All Charts", "bundle:inputs", "package:api", "package:api.extra", "package:no-chart"}) {
		t.Fatalf("package nodes should start collapsed: %v", got)
	}
	m.scopeTreeKey = "package:api"
	m.scopeTreeRight()
	if !m.scopeExpanded["package:api"] || m.scopeTreeKey != "package:api" {
		t.Fatal("right should expand the selected package first")
	}
	m.scopeTreeRight()
	if m.scopeTreeKey != "component:api/server" {
		t.Fatal("right on an expanded package should enter its component")
	}
	m.scopeTreeRight()
	if m.scopeTreeKey != "api/server/app" {
		t.Fatal("right should navigate to the actual chart")
	}
	m.scopeTreeLeft()
	m.scopeTreeLeft()
	component, _ := m.workspaceScope("component:api/server")
	if m.scopeTreeKey != "component:api/server" || m.scopeIsExpanded(component) {
		t.Fatal("left should return to and collapse the component")
	}
	m.currentChart = "api/server/app"
	m.syncScopeCursor()
	if m.scopeTreeKey != m.currentChart || !m.scopeExpanded["component:api/server"] || m.scopeCursor != 9 {
		t.Fatal("sync should reveal active ancestors without changing the picker cursor")
	}
	m.moveScopeBoundary(false)
	if m.scopeTreeKey != "All Charts" {
		t.Fatal("home did not select the tree root")
	}
	m.moveScopeBoundary(true)
	if m.scopeTreeKey != "package:no-chart" {
		t.Fatal("end did not select the last visible scope")
	}
	m.moveScopeCursor(-1)
	if m.scopeTreeKey != "package:api.extra" {
		t.Fatal("cursor movement skipped visible scope identities")
	}
}

func TestScopeTreeRetainsLegacyOverrideOnlyPackage(t *testing.T) {
	context := introspect.UDSOverrideContext{PackageName: "legacy-only", ComponentName: "server", ChartName: "unavailable"}
	report := introspect.InspectionReport{
		UDSOverrideValues: []introspect.UDSOverrideValueWithContext{{
			UDSOverrideContext: context,
			UDSOverrideValue:   introspect.UDSOverrideValue{Path: "replicas", Value: 3},
		}},
		UDSOverrideVariables: []introspect.UDSOverrideVariableWithContext{{
			UDSOverrideContext:  context,
			UDSOverrideVariable: introspect.UDSOverrideVariable{Name: "IMAGE", Path: "image", Default: "example:v1"},
		}},
	}
	m := NewModel(report)
	if got := scopeTreeKeys(m.scopes); !reflect.DeepEqual(got, []string{"All Charts", "package:legacy-only"}) {
		t.Fatalf("legacy override context lost its chartless package owner: %v", got)
	}
	if m.showUDSSummary || len(m.filteredValues) != 2 || !m.filteredValues[0].IsUDSOverrideValue || !m.filteredValues[1].IsUDSOverrideVar {
		t.Fatalf("default Settings catalog lost orphan fixed or variable overrides: %+v", m.filteredValues)
	}
	m.scopeTreeKey = "package:legacy-only"
	if !m.applyScopeCursor() || len(m.filteredValues) != 2 || m.chartInScope("legacy-only/server/unavailable") {
		t.Fatal("known package overrides should remain visible without inventing an available destination chart")
	}
	for _, item := range m.filteredValues {
		if !item.IsSummaryItem || item.PackageName != "legacy-only" || item.SummaryComponent != "legacy-only/server/unavailable" {
			t.Fatalf("orphan override lost its declaration identity: %+v", item)
		}
	}
	m.filterInput.SetValue("pkg:legacy-only")
	m.updateFilteredValues()
	if len(m.filteredValues) != 2 {
		t.Fatal("package filter lost known orphan override owners")
	}
	for range 2 {
		m.toggleUDSSummary()
		if len(m.filteredValues) != 2 {
			t.Fatal("U must not hide or duplicate the only available override row")
		}
	}
	m.currentChart = "package:legacy"
	m.updateFilteredValues()
	if len(m.filteredValues) != 0 {
		t.Fatal("legacy package ID was matched by substring")
	}
}

func TestScopeTreeOverrideSummaryOnlyDuplicatesAvailableDestinationRows(t *testing.T) {
	context := introspect.UDSOverrideContext{PackageName: "legacy", ComponentName: "server", ChartName: "app"}
	fixed := introspect.UDSOverrideValue{Path: "replicas", Value: 3}
	variable := introspect.UDSOverrideVariable{Name: "CONFIG", Path: "image", Default: "example:v1"}
	report := introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"legacy/server/app": {UDSOverrideValues: []introspect.UDSOverrideValue{fixed}, UDSOverrideVariables: []introspect.UDSOverrideVariable{variable}},
			"legacy/server/nil": nil,
		},
		UDSOverrideValues: []introspect.UDSOverrideValueWithContext{
			{UDSOverrideContext: context, UDSOverrideValue: fixed},
			{UDSOverrideContext: context, UDSOverrideValue: introspect.UDSOverrideValue{Path: "service.port", Value: 8080}},
			{UDSOverrideContext: introspect.UDSOverrideContext{PackageName: "legacy", ComponentName: "server", ChartName: "nil"}, UDSOverrideValue: introspect.UDSOverrideValue{Path: "pending", Value: true}},
		},
		UDSOverrideVariables: []introspect.UDSOverrideVariableWithContext{
			{UDSOverrideContext: context, UDSOverrideVariable: variable},
			{UDSOverrideContext: context, UDSOverrideVariable: introspect.UDSOverrideVariable{Name: "CONFIG", Path: "service.name", Default: "app"}},
		},
	}
	m := NewModel(report)
	for _, summaryOn := range []bool{false, true, false} {
		if m.showUDSSummary != summaryOn {
			m.toggleUDSSummary()
		}
		counts, summaries := map[string]int{}, map[string]int{}
		for _, item := range m.filteredValues {
			if !item.IsUDSOverrideValue && !item.IsUDSOverrideVar {
				continue
			}
			key := item.Path
			if item.IsUDSOverrideVar {
				key += "/" + item.UDSVarPath
			}
			counts[key]++
			if item.IsSummaryItem {
				summaries[key]++
			}
		}
		for _, key := range []string{"service.port", "pending", "CONFIG/service.name"} {
			if counts[key] != 1 || summaries[key] != 1 {
				t.Fatalf("U=%t lost or duplicated orphan %s: rows=%v summaries=%v", summaryOn, key, counts, summaries)
			}
		}
		wantCopies, wantSummaries := 1, 0
		if summaryOn {
			wantCopies, wantSummaries = 2, 1
		}
		for _, key := range []string{"replicas", "CONFIG/image"} {
			if counts[key] != wantCopies || summaries[key] != wantSummaries {
				t.Fatalf("U=%t mishandled available destination %s: rows=%v summaries=%v", summaryOn, key, counts, summaries)
			}
		}
	}
}

func TestScopeTreeApplyClearsStaleInspectorAndKeepsBundleInputsSeparate(t *testing.T) {
	m := scopedWorkspaceModel()
	m.allCategories = true
	stale := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "old"}}
	m.detailRef = &stale
	m.mapping = &mappingScreen{}
	m.mappingRowDetail = true
	m.traceHistory = []traceReturn{{ref: &stale}}
	m.detailPanel = detailSchema
	m.scopeTreeKey = "package:no-chart"
	if !m.applyScopeCursor() || m.currentChart != "package:no-chart" || m.focusPane != FocusValues {
		t.Fatal("scope apply did not select the exact chartless package")
	}
	if m.mapping != nil || m.mappingRowDetail || len(m.traceHistory) != 0 || m.detailPanel != detailSummary || m.cursor != 0 || m.viewportOffset != 0 {
		t.Fatal("scope apply retained stale mapping or inspector context")
	}
	if selected, ok := m.detailValue(); ok && selected.Path == "old" {
		t.Fatal("inspector stayed pinned to a value from the previous scope")
	}
	m.currentChart = "bundle:inputs"
	if m.chartInScope("api/server/app") || m.itemInScope(DisplayItem{IsPackageValue: true, Component: "Zarf Package Values", PackageName: "api"}) {
		t.Fatal("bundle inputs scope admitted package/chart settings")
	}
	if !m.itemInScope(DisplayItem{IsBundleVariable: true}) || !m.itemInScope(DisplayItem{IsPackageValue: true, Component: "UDS Next bundle", PackageName: "api"}) {
		t.Fatal("bundle inputs scope lost variables or package declarations")
	}
}

func TestScopeTreePickerUsesWidePaneAndRestoresOverlayCaller(t *testing.T) {
	m := scopedWorkspaceModel()
	m.windowWidth, m.windowHeight = 120, 30
	m.focusPane = FocusInspector
	m.currentChart = "api/server/app"
	m.syncScopeCursor()
	m.beginScopePicker()
	if m.inputMode != InputModeNormal || m.focusPane != FocusComponents || m.scopeTreeKey != m.currentChart {
		t.Fatal("wide scope navigation did not focus the persistent hierarchy")
	}
	for _, width := range []int{60, 100} {
		m.windowWidth, m.focusPane = width, FocusInspector
		m.beginScopePicker()
		if m.inputMode != InputModeScopeSelect {
			t.Fatal("narrow scope navigation should use the searchable overlay")
		}
		updated, _ := m.handleScopeKey(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(Model)
		if m.inputMode != InputModeNormal || m.focusPane != FocusInspector || m.currentChart != "api/server/app" {
			t.Fatal("overlay cancellation lost the original focus or scope")
		}
	}
	m.focusPane = FocusComponents
	for _, size := range [][2]int{{24, 12}, {32, 20}} {
		body := m.scopeTreePane(size[0], size[1])
		if lipgloss.Width(body) > size[0] || lipgloss.Height(body) > size[1] || !strings.Contains(ansi.Strip(body), "Scope") {
			t.Fatalf("scope tree exceeded pane dimensions %v:\n%s", size, body)
		}
	}
}

func TestScopeTreeReopenPreservesUnappliedCursorAndFolds(t *testing.T) {
	for _, navigation := range []struct{ open, close string }{{"f", "tab"}, {"f", "esc"}, {"shift+tab", "tab"}, {"shift+tab", "esc"}} {
		t.Run(navigation.open+"/"+navigation.close, func(t *testing.T) {
			m := scopedWorkspaceModel()
			resizeWorkspace(&m, 160, 30)
			m.currentChart = "api/server/app"
			m.filterInput.SetValue("kind:helm")
			m.updateFilteredValues()
			m.syncScopeCursor()
			pressWorkspaceKey(&m, "j")
			selected := m.inspectorSelection
			cursor := m.cursor
			pressWorkspaceKey(&m, navigation.open)
			pressWorkspaceKey(&m, "left")
			pressWorkspaceKey(&m, "left")
			if m.scopeTreeKey != "component:api/server" || m.scopeExpanded[m.scopeTreeKey] {
				t.Fatal("fixture did not leave an unapplied cursor on a collapsed component")
			}
			expanded := make(map[string]bool, len(m.scopeExpanded))
			for key, value := range m.scopeExpanded {
				expanded[key] = value
			}
			pressWorkspaceKey(&m, navigation.close)
			if m.focusPane != FocusValues || m.workspaceGeometry(m.windowWidth).scope != 0 {
				t.Fatal("leaving Scope did not return its space to Settings and Inspector")
			}
			pressWorkspaceKey(&m, navigation.open)
			if m.focusPane != FocusComponents || m.workspaceGeometry(m.windowWidth).scope == 0 ||
				m.scopeTreeKey != "component:api/server" || !reflect.DeepEqual(m.scopeExpanded, expanded) ||
				m.currentChart != "api/server/app" || m.filterInput.Value() != "kind:helm" ||
				m.cursor != cursor || m.inspectorSelection != selected {
				t.Fatal("reopening Scope changed its unapplied cursor, folds, applied scope, or selected setting")
			}
		})
	}
}
