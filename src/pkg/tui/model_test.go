package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
)

func TestGenerateBundleSnippetRequiresBundleContextByDefault(t *testing.T) {
	snippet := generateBundleSnippet("component/chart", "image.tag", "1.0.0")
	if snippet != "" {
		t.Fatalf("expected no snippet outside UDS bundle context, got:\n%s", snippet)
	}
}

func TestGenerateBundleSnippetForZarfPackageBundleMode(t *testing.T) {
	m := Model{detailBundleMode: true}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path:         "image.tag",
			DefaultValue: "1.0.0",
		},
		Component: "podinfo/podinfo",
	}

	snippet := m.generateBundleSnippetForItem(v)
	for _, want := range []string{
		"- name: <package-name>",
		"      podinfo:",
		"        podinfo:",
		"            - name: <prefix>_IMAGE_TAG",
		"              path: image.tag",
		"              default: 1.0.0",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("expected snippet to contain %q, got:\n%s", want, snippet)
		}
	}
}

func TestGenerateBundleSnippetForHelmChartBundleModeUsesChartName(t *testing.T) {
	m := Model{
		detailBundleMode: true,
		components: map[string]*introspect.IntrospectResult{
			"": {ChartName: "istiod"},
		},
	}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path:         "global.proxy.resources.requests.memory",
			DefaultValue: "128Mi",
		},
		Component: "",
	}

	snippet := m.generateBundleSnippetForItem(v)
	for _, want := range []string{
		"- name: <package-name>",
		"      <component-name>:",
		"        istiod:",
		"            - name: <prefix>_GLOBAL_PROXY_RESOURCES_REQUESTS_MEMORY",
		"              default: 128Mi",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("expected snippet to contain %q, got:\n%s", want, snippet)
		}
	}
}

func TestGenerateBundleSnippetWithComplexYAMLDefault(t *testing.T) {
	m := Model{detailBundleMode: true}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path: "resources.limits",
			DefaultValue: map[string]interface{}{
				"cpu":    "500m",
				"memory": "512Mi",
			},
		},
		Component: "pkg/comp/chart",
	}

	snippet := m.generateBundleSnippetForItem(v)
	if strings.Contains(snippet, "{") || strings.Contains(snippet, "}") {
		t.Fatalf("expected block YAML instead of JSON flow formatting, got:\n%s", snippet)
	}
	for _, want := range []string{
		"default:",
		"  cpu: 500m",
		"  memory: 512Mi",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("expected snippet to contain %q, got:\n%s", want, snippet)
		}
	}
}

func TestWorkspaceResponsiveLayoutsFitTerminal(t *testing.T) {
	for _, test := range []struct {
		name              string
		width, height     int
		want, doesNotWant []string
	}{
		{"wide", 132, 32, []string{"Scope", "Settings", "Inspector"}, []string{"CATEGORIES"}},
		{"medium", 100, 28, []string{"Settings", "Inspector"}, []string{"CATEGORIES", "COMPONENTS"}},
		{"compact", 72, 24, []string{"Settings"}, []string{"Inspector", "COMPONENTS"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := testWorkspaceModel()
			resizeWorkspace(&model, test.width, test.height)
			view := assertWorkspaceSize(t, model, test.width, test.height)
			for _, want := range test.want {
				if !strings.Contains(view, want) {
					t.Errorf("view does not contain %q", want)
				}
			}
			for _, unwanted := range test.doesNotWant {
				if strings.Contains(view, unwanted) {
					t.Errorf("view unexpectedly contains %q", unwanted)
				}
			}
		})
	}
}

func TestWorkspaceFooterDoesNotShowFocusedPaneLabel(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 132
	for _, pane := range []FocusPane{FocusComponents, FocusValues, FocusInspector} {
		model.focusPane = pane
		footer := model.workspaceFooter(model.windowWidth)
		if strings.Contains(footer, "focused") {
			t.Errorf("pane %v: footer unexpectedly shows focus label: %q", pane, footer)
		}
	}
}

func TestValuesPaneUsesLastAvailableRow(t *testing.T) {
	model := testWorkspaceModel()
	model.windowHeight = 18
	model.filteredValues = make([]DisplayItem, 30)
	for i := range model.filteredValues {
		model.filteredValues[i] = DisplayItem{
			ValueUsage: introspect.ValueUsage{Path: fmt.Sprintf("value-%02d", i)},
			Component:  "core-base/istio-controlplane/istiod",
		}
	}
	for _, width := range []int{72, 100, 132} {
		model.windowWidth = width
		model.viewportOffset = 0
		lines := strings.Split(model.View(), "\n")
		if got := lines[model.windowHeight-3]; !strings.Contains(got, "value-") {
			t.Fatalf("width %d: last Values interior row should contain a value, got %q", width, got)
		}
		updated, _ := model.Update(key("G"))
		model = updated.(Model)
		view := model.View()
		if got := lipgloss.Height(view); got != model.windowHeight {
			t.Fatalf("width %d: scrolled workspace height %d, want %d", width, got, model.windowHeight)
		}
		if got := lipgloss.Width(view); got != width {
			t.Fatalf("width %d: scrolled workspace width %d", width, got)
		}
		if !strings.Contains(strings.Split(view, "\n")[0], "NEUTRINO") {
			t.Fatalf("width %d: scrolled workspace lost its title", width)
		}
	}
}

func TestKeyboardFlowFocusesInspectorWithinWorkspace(t *testing.T) {
	model := testWorkspaceModel()
	resizeWorkspace(&model, 132, 32)
	selected := model.cursor
	pressWorkspaceKey(&model, "enter")
	view := model.View()
	if model.focusPane != FocusInspector || model.detailPanel != detailSummary || !strings.Contains(view, "Helm value") {
		t.Fatalf("Enter did not focus the selected summary:\n%s", view)
	}
	for _, pane := range []string{"Settings", "Inspector"} {
		if !strings.Contains(view, pane) {
			t.Fatalf("focusing Inspector removed %s", pane)
		}
	}
	if model.workspaceGeometry(model.windowWidth).scope != 0 || !strings.Contains(view, "f Scope") {
		t.Fatal("Inspector focus should leave Scope collapsed and discoverable")
	}
	pressWorkspaceKey(&model, "esc")
	if model.focusPane != FocusValues || model.cursor != selected || !strings.Contains(model.View(), "Inspector") {
		t.Fatal("Escape should return focus to Settings without hiding Inspector")
	}
}

func TestCommandPaletteRunsCopyPathWithoutMouse(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28

	updated, _ := model.Update(key(":"))
	palette := updated.(Model)
	if view := palette.View(); !strings.Contains(view, "COMMAND PALETTE") || !strings.Contains(view, "Copy value path") {
		t.Fatalf("command palette does not expose copy action:\n%s", view)
	}

	filtered := palette
	for _, char := range "copy" {
		updated, _ = filtered.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		filtered = updated.(Model)
	}
	updated, cmd := filtered.Update(key("enter"))
	if cmd == nil {
		t.Fatal("copy command did not emit an OSC 52 command")
	}
	if view := updated.(Model).View(); !strings.Contains(view, "Copied global.proxy.resources.requests.memory") {
		t.Fatalf("copy command did not report the copied path:\n%s", view)
	}
}
func TestStatusReturnsToDefaultAfterTwoSeconds(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28

	updated, _ := model.Update(key("Z"))
	model = updated.(Model)
	if !strings.Contains(model.workspaceFooter(100), "Package variable summary on") {
		t.Fatal("toggle did not display its status")
	}

	updated, _ = model.Update(pathScrollTickMsg(model.statusExpiresAt.Add(-time.Nanosecond)))
	model = updated.(Model)
	if !strings.Contains(model.workspaceFooter(100), "Package variable summary on") {
		t.Fatal("status disappeared before two seconds")
	}

	updated, _ = model.Update(pathScrollTickMsg(model.statusExpiresAt))
	model = updated.(Model)
	if strings.Contains(model.workspaceFooter(100), "Package variable summary on") ||
		!strings.Contains(model.workspaceFooter(100), "tab") {
		t.Fatalf("status did not return to default footer: %q", model.workspaceFooter(100))
	}
}

func TestNewStatusRefreshesExpiry(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100

	updated, _ := model.Update(key("Z"))
	model = updated.(Model)
	// Simulate an earlier status without depending on wall-clock resolution.
	model.statusExpiresAt = model.statusExpiresAt.Add(-time.Second)
	oldExpiry := model.statusExpiresAt
	updated, _ = model.Update(key("U"))
	model = updated.(Model)
	updated, _ = model.Update(pathScrollTickMsg(oldExpiry))
	model = updated.(Model)
	if !strings.Contains(model.workspaceFooter(100), "Bundle override summary on") {
		t.Fatalf("previous message expired the newer status: %q", model.workspaceFooter(100))
	}
}

func TestAllSettingsAreGroupedByTypeAndOwner(t *testing.T) {
	const first = "core-base/istio-controlplane/istiod"
	const second = "core-base/istio-admin-gateway/gateway"
	model := Model{
		components: map[string]*introspect.IntrospectResult{
			first:  {ChartName: "istiod"},
			second: {ChartName: "gateway"},
		},
		chartNames:    []string{"All Charts", first, second},
		currentChart:  "All Charts",
		allCategories: true,
		filteredValues: []DisplayItem{
			{ValueUsage: introspect.ValueUsage{Path: "REGISTRY_URL"}, Component: "Zarf Package Variables", IsPkgVar: true},
			{ValueUsage: introspect.ValueUsage{Path: "IMAGE_TAG"}, Component: first, IsZarfVar: true},
			{ValueUsage: introspect.ValueUsage{Path: "PROXY_MEMORY"}, Component: first, IsUDSOverrideVar: true, UDSVarPath: "global.proxy.resources.requests.memory"},
			{ValueUsage: introspect.ValueUsage{Path: "image.repository"}, Component: first},
			{ValueUsage: introspect.ValueUsage{Path: "replicas"}, Component: second, IsUDSOverrideValue: true},
		},
		focusPane:    FocusValues,
		windowWidth:  110,
		windowHeight: 30,
	}

	view := model.valuesPane(110, 28)
	headings := []string{"━━ Zarf variables ━", "Zarf variables · " + first,
		"Legacy overrides · " + first, "Helm values · " + first,
		"Legacy overrides · " + second}
	previous := -1
	for _, heading := range headings {
		index := strings.Index(view, heading)
		if index < 0 {
			t.Fatalf("missing heading %q:\n%s", heading, view)
		}
		if index <= previous {
			t.Fatalf("heading %q is out of order:\n%s", heading, view)
		}
		previous = index
	}
	if strings.Count(view, first) != 3 {
		t.Fatalf("each distinct configuration layer must retain the same destination owner:\n%s", view)
	}
	if got := model.valueLineCount(1, 2); got != 4 {
		t.Fatalf("distinct layers sharing an owner need separate headings: got %d lines, want 4", got)
	}
	model.viewportOffset = 2
	scrolled := model.valuesPane(110, 28)
	if !strings.Contains(scrolled, "Legacy overrides · "+first) || !strings.Contains(scrolled, "PROXY_MEMORY") {
		t.Fatalf("scrolling into a layer must retain its meaning and destination:\n%s", scrolled)
	}
}

func TestWorkspaceColumnWidthsExpandSettingsWhileScopeIsClosed(t *testing.T) {
	model := testWorkspaceModel()
	resizeWorkspace(&model, 120, 32)
	for _, layout := range []struct {
		focus                      FocusPane
		scope, settings, inspector int
	}{{FocusValues, 0, 66, 54}, {FocusComponents, 24, 46, 50}, {FocusInspector, 0, 66, 54}} {
		model.focusPane = layout.focus
		view := model.View()
		if !strings.Contains(workspaceColumns(view, layout.scope, layout.settings), "Settings") ||
			!strings.Contains(workspaceColumns(view, layout.scope+layout.settings, layout.inspector), "Inspector") ||
			model.workspaceGeometry(120) != (workspaceGeometry{scope: layout.scope, settings: layout.settings, inspector: layout.inspector}) {
			t.Fatalf("focus %v did not use the requested pane geometry:\n%s", layout.focus, view)
		}
	}
}

func TestSettingsArrowsSelectTypesAndLettersNavigatePanes(t *testing.T) {
	model := testWorkspaceModel()
	resizeWorkspace(&model, 120, 28)
	originalCategory := model.category
	pressWorkspaceKey(&model, "right")
	if model.focusPane != FocusValues || model.allCategories || model.category != (originalCategory+1)%8 {
		t.Fatal("right arrow did not select the next Settings type")
	}
	pressWorkspaceKey(&model, "left")
	if model.focusPane != FocusValues || model.allCategories || model.category != originalCategory {
		t.Fatal("left arrow did not restore the Settings type")
	}
	pressWorkspaceKey(&model, "l")
	if model.focusPane != FocusInspector {
		t.Fatal("l did not focus Inspector")
	}
	pressWorkspaceKey(&model, "h")
	if model.focusPane != FocusValues {
		t.Fatal("h did not return to Settings")
	}
	pressWorkspaceKey(&model, "h")
	if model.focusPane != FocusComponents {
		t.Fatal("h did not focus Scope")
	}
}

func TestSelectedLongPathAutoScrolls(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 72
	model.windowHeight = 24
	model.focusPane = FocusValues
	model.pathScroll = 0
	model.pathScrollPause = 0

	initial := model.View()
	if !strings.Contains(initial, "global.proxy") {
		t.Fatalf("initial path does not show its beginning:\n%s", initial)
	}

	model.stepPathScroll()
	if model.pathScroll <= 0 {
		t.Fatalf("expected pathScroll to advance automatically, got %d", model.pathScroll)
	}

	scrolled := model.View()
	if !strings.Contains(scrolled, "‹") {
		t.Fatalf("view did not render scrolled path with scroll indicator:\n%s", scrolled)
	}
}

func TestLineAndComponentNavigation(t *testing.T) {
	compA := "core-base/istio-controlplane/istiod"
	compB := "core-base/monitoring/grafana"
	compC := "core-base/monitoring/prometheus"

	model := Model{
		components: map[string]*introspect.IntrospectResult{
			compA: {ChartName: "istiod"},
			compB: {ChartName: "grafana"},
			compC: {ChartName: "prometheus"},
		},
		chartNames:   []string{"All Charts", compA, compB, compC},
		currentChart: "All Charts",
		filteredValues: []DisplayItem{
			{ValueUsage: introspect.ValueUsage{Path: "a.1"}, Component: compA},
			{ValueUsage: introspect.ValueUsage{Path: "a.2"}, Component: compA},
			{ValueUsage: introspect.ValueUsage{Path: "a.3"}, Component: compA},
			{ValueUsage: introspect.ValueUsage{Path: "b.1"}, Component: compB},
			{ValueUsage: introspect.ValueUsage{Path: "b.2"}, Component: compB},
			{ValueUsage: introspect.ValueUsage{Path: "b.3"}, Component: compB},
			{ValueUsage: introspect.ValueUsage{Path: "b.4"}, Component: compB},
			{ValueUsage: introspect.ValueUsage{Path: "c.1"}, Component: compC},
			{ValueUsage: introspect.ValueUsage{Path: "c.2"}, Component: compC},
			{ValueUsage: introspect.ValueUsage{Path: "c.3"}, Component: compC},
		},
		focusPane:    FocusValues,
		windowWidth:  100,
		windowHeight: 28,
	}

	// 1. 'j' moves 1 line down
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m := updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1 after 'j', got %d", m.cursor)
	}

	// 2. 'k' moves 1 line up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0 after 'k', got %d", m.cursor)
	}

	// 3. 'J' jumps 5 lines down
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	m = updated.(Model)
	if m.cursor != 5 {
		t.Fatalf("expected cursor 5 after 'J', got %d", m.cursor)
	}

	// 4. 'K' jumps 5 lines up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0 after 'K', got %d", m.cursor)
	}

	// 'n' jumps to the next component (from compA to compB at index 3).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.cursor != 3 {
		t.Fatalf("expected cursor 3 (compB) after 'n', got %d", m.cursor)
	}

	// ']' remains an alias for the next component (compC at index 7).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = updated.(Model)
	if m.cursor != 7 {
		t.Fatalf("expected cursor 7 (compC) after ']', got %d", m.cursor)
	}

	// Shift+N jumps to the previous component (from compC to compB).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	m = updated.(Model)
	if m.cursor != 3 {
		t.Fatalf("expected cursor 3 (compB) after 'N', got %d", m.cursor)
	}

	// '[' remains an alias; in the middle of compB it jumps to its start.
	m.cursor = 5
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	m = updated.(Model)
	if m.cursor != 3 {
		t.Fatalf("expected cursor 3 (start of compB) after '[', got %d", m.cursor)
	}
}

func TestCommandPaletteShowsKeyBeforeAction(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28
	updated, _ := model.Update(key(":"))
	view := updated.(Model).View()
	if !strings.Contains(view, "KEY      ACTION") || !strings.Contains(view, "/        Filter values") {
		t.Fatalf("command palette does not show key before action:\n%s", view)
	}
}

func TestDetailsViewRendersCopyableYAMLDefault(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28
	model.filteredValues[0].DefaultValue = map[string]interface{}{
		"requests": map[string]interface{}{
			"cpu":    "100m",
			"memory": "128Mi",
		},
	}
	model.focusPane = FocusInspector
	model.buildDetailItems()
	openInspectorTestPanel(t, &model, detailFullValue)

	view := workspaceColumns(model.View(), 45, 55)
	if strings.Contains(view, "{requests:") || strings.Contains(view, "map[") {
		t.Fatalf("expected copyable YAML block instead of JSON or Go struct, got:\n%s", view)
	}
	for _, want := range []string{
		"requests:",
		"  cpu: 100m",
		"  memory: 128Mi",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected Details view to contain %q, got:\n%s", want, view)
		}
	}
}

func TestDetailsKeepFullVariableDescriptions(t *testing.T) {
	description := "A description that continues past the old sixty-byte cutoff " +
		strings.Repeat("x", 70) + "\nThe second paragraph is still visible."
	for _, tc := range []struct {
		name  string
		setup func(*Model)
		match func(DisplayItem) bool
	}{
		{
			name: "package variable",
			setup: func(m *Model) {
				m.pkgVariables = []v1alpha1.InteractiveVariable{{Variable: v1alpha1.Variable{Name: "LONG_DESC"}, Description: description}}
				m.showZarfSummary = true
			},
			match: func(item DisplayItem) bool { return item.IsPkgVar },
		},
		{
			name: "bundle summary variable",
			setup: func(m *Model) {
				m.udsOverrideVariables = []introspect.UDSOverrideVariableWithContext{{
					UDSOverrideContext: introspect.UDSOverrideContext{
						PackageName: "core-base", ComponentName: "istio-controlplane", ChartName: "istiod",
					},
					UDSOverrideVariable: introspect.UDSOverrideVariable{Name: "LONG_DESC", Description: description},
				}}
				m.showUDSSummary = true
			},
			match: func(item DisplayItem) bool { return item.IsUDSOverrideVar && item.IsSummaryItem },
		},
		{
			name: "component variable",
			setup: func(m *Model) {
				m.components[m.chartNames[1]].UDSOverrideVariables = []introspect.UDSOverrideVariable{{
					Name: "LONG_DESC", Description: description,
				}}
			},
			match: func(item DisplayItem) bool { return item.IsUDSOverrideVar && !item.IsSummaryItem },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := testWorkspaceModel()
			tc.setup(&model)
			model.updateFilteredValues()
			model.selectSettingType(7)
			for index, item := range model.filteredValues {
				if !tc.match(item) {
					continue
				}
				model.cursor = index
				model.focusPane = FocusInspector
				model.buildDetailItems()
				openInspectorTestPanel(t, &model, detailDescription)
				body, _ := model.inspectorContent(48, true)
				if !strings.Contains(body, "The second paragraph is still visible.") ||
					!strings.Contains(body, strings.Repeat("x", 34)) || strings.Contains(body, "cuto...") {
					t.Fatalf("description was lost rather than wrapped:\n%s", body)
				}
				model.windowWidth = 52
				model.windowHeight = 28
				model.focusPane = FocusInspector
				model.buildDetailItems()
				if view := model.View(); !strings.Contains(view, "The second paragraph is still visible.") {
					t.Fatalf("details viewport did not show the complete description:\n%s", view)
				}
				return
			}
			t.Fatal("variable not present in filtered values")
		})
	}
}

func TestWholeNumbersRenderAsIntegersNotFloats(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28
	model.filteredValues[0].DefaultValue = float64(3000)
	model.focusPane = FocusInspector
	model.buildDetailItems()

	view := model.View()
	if strings.Contains(view, "3000.0") {
		t.Fatalf("expected 3000 as an integer, got 3000.0 in Details view:\n%s", view)
	}
	if !strings.Contains(view, "3000") {
		t.Fatalf("expected Details view to contain 3000, got:\n%s", view)
	}

	model.filteredValues[0].DefaultValue = map[string]interface{}{
		"port":    float64(3000),
		"timeout": float64(30),
	}
	model.buildDetailItems()
	openInspectorTestPanel(t, &model, detailFullValue)
	nestedView := model.View()
	if strings.Contains(nestedView, "3000.0") || strings.Contains(nestedView, "30.0") {
		t.Fatalf("expected integers in nested map, got float representation:\n%s", nestedView)
	}
	if !strings.Contains(nestedView, "port: 3000") || !strings.Contains(nestedView, "timeout: 30") {
		t.Fatalf("expected nested map to contain port: 3000 and timeout: 30, got:\n%s", nestedView)
	}

	m := Model{detailBundleMode: true}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path:         "service.port",
			DefaultValue: float64(3000),
		},
		Component: "pkg/comp/chart",
	}
	snippet := m.generateBundleSnippetForItem(v)
	if strings.Contains(snippet, "3000.0") {
		t.Fatalf("expected snippet to have default: 3000, got float 3000.0:\n%s", snippet)
	}
	if !strings.Contains(snippet, "default: 3000") {
		t.Fatalf("expected snippet to contain default: 3000, got:\n%s", snippet)
	}
}

func TestLongScopeNamesStayWithinTheirPane(t *testing.T) {
	longComp := "core-base/istio-controlplane/istiod-with-a-very-long-name-that-does-not-fit-in-the-sidebar"
	model := NewModel(introspect.InspectionReport{
		Components:     map[string]*introspect.IntrospectResult{longComp: {ChartName: "istiod", Values: []introspect.ValueUsage{{Path: "enabled", DefaultValue: true}}}},
		ComponentOrder: []string{longComp},
	})
	model.currentChart = longComp
	model.updateFilteredValues()
	model.focusPane = FocusComponents
	resizeWorkspace(&model, 120, 28)
	view := assertWorkspaceSize(t, model, 120, 28)
	if !strings.Contains(workspaceColumns(view, 0, 24), "Scope") || !strings.Contains(workspaceColumns(view, 24, 46), "Settings") || model.currentChart != longComp {
		t.Fatalf("long chart name changed scope identity or pane boundaries:\n%s", view)
	}
}

func TestBundleAuthoringHasSeparateScrollablePage(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 72
	model.windowHeight = 12
	for line := 1; line <= 12; line++ {
		model.filteredValues[0].Locations = append(model.filteredValues[0].Locations, introspect.ValueUsageLocation{
			File: fmt.Sprintf("/tmp/chart/templates/%02d.yaml", line), Line: line, Column: 1, Kind: "field",
		})
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if strings.Contains(model.View(), "uds-bundle.yaml") {
		t.Fatal("summary should not contain the authoring example")
	}
	updated, _ = model.Update(key("b"))
	model = updated.(Model)
	if model.detailPanel != detailAuthoring || !strings.Contains(model.View(), "Authoring example") {
		t.Fatalf("b did not open the authoring page:\n%s", model.View())
	}
	for range 16 {
		updated, _ = model.Update(key("j"))
		model = updated.(Model)
	}
	if !strings.Contains(model.View(), "default: 128Mi") {
		t.Fatalf("authoring example cannot be scrolled to its last line:\n%s", model.View())
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	if model.detailPanel != detailSummary || model.focusPane != FocusInspector || model.detailScrollOffset != 0 {
		t.Fatal("escape did not restore the compact summary")
	}
}

func TestDetailsSourcesUseFriendlyLabelsAndPreserveFileIdentity(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 110
	model.windowHeight = 32
	source := t.TempDir()
	model.components["core-base/istio-controlplane/istiod"].Source = source
	locations := []introspect.ValueUsageLocation{
		{File: filepath.Join(source, "values.yaml"), Line: 4, Column: 1},
		{File: filepath.Join(source, "charts", "child", "templates", "workload.yaml"), Line: 7, Column: 2},
		{File: filepath.Join(source, "templates", "deployment.yaml"), Line: 148, Column: 12},
	}
	for _, location := range locations {
		writeModelTestSource(t, location.File)
	}
	model.filteredValues[0].Locations = locations
	model.focusPane = FocusInspector
	model.buildDetailItems()
	body, _ := model.inspectorContent(106, true)
	if strings.Count(body, "Sources (3)") != 1 || strings.Contains(body, source) || strings.Contains(body, "workload.yaml") {
		t.Fatalf("summary must offer one compact Sources action without inline paths:\n%s", body)
	}
	openInspectorTestPanel(t, &model, detailSources)
	body, _ = model.inspectorContent(106, true)
	if len(model.detailItems) != len(locations) {
		t.Fatalf("source list lost recorded occurrences: %+v", model.detailItems)
	}
	for index, location := range locations {
		if model.detailItems[index].Location != location || model.detailItems[index].File != location.File || !strings.Contains(strings.Join(strings.Fields(body), ""), fmt.Sprintf("%s:%d", filepath.Base(location.File), location.Line)) || strings.Contains(body, source) {
			t.Fatalf("source %d lost its location or filename-and-line label: %+v\n%s", index, model.detailItems, body)
		}
		model.detailCursor = index
		updated, _ := model.Update(key("enter"))
		model = updated.(Model)
		technical, _ := model.inspectorContent(106, true)
		if model.detailPanel != detailSourceFile || !strings.Contains(strings.Join(strings.Fields(technical), ""), location.File) || len(model.detailItems) != 1 ||
			model.detailItems[0].File != location.File || model.detailItems[0].Location != location {
			t.Fatalf("source details lost exact editor target: %+v\n%s", model.detailItems, technical)
		}
		updated, _ = model.Update(key("esc"))
		model = updated.(Model)
		if model.detailPanel != detailSources || model.detailCursor != index {
			t.Fatal("source details did not restore source selection")
		}
	}
}

func TestDetailsKeepsSelectedSourceVisibleWhileScrolling(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 72
	model.windowHeight = 14
	source := model.components["core-base/istio-controlplane/istiod"].Source
	for line := 1; line <= 15; line++ {
		model.filteredValues[0].Locations = append(model.filteredValues[0].Locations, introspect.ValueUsageLocation{
			File: filepath.Join(source, "templates", "subdir", fmt.Sprintf("%02d.yaml", line)), Line: line, Column: 2,
		})
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	openInspectorTestPanel(t, &model, detailSources)
	for range 15 {
		updated, _ = model.Update(key("j"))
		model = updated.(Model)
	}
	if model.detailCursor != 15 {
		t.Fatalf("selected action = %d, want 15", model.detailCursor)
	}
	if !strings.Contains(model.View(), "> 15.yaml:15") || model.detailItems[model.detailCursor].File != filepath.Join(source, "templates", "subdir", "15.yaml") {
		t.Fatalf("selected source should remain visible with its filename and line:\n%s", model.View())
	}
	for range 15 {
		updated, _ = model.Update(key("k"))
		model = updated.(Model)
	}
	if !strings.Contains(model.View(), "> deployment.yaml:148") || model.detailItems[model.detailCursor].File != filepath.Join(source, "templates", "deployment.yaml") {
		t.Fatalf("first source should be visible again:\n%s", model.View())
	}
}

func TestDetailsRendersVeryLongScalarValue(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28
	model.filteredValues[0].DefaultValue = strings.Repeat("x", 100_000)
	model.focusPane = FocusInspector
	model.buildDetailItems()

	view := model.View()
	if got := lipgloss.Width(view); got != model.windowWidth {
		t.Fatalf("Details width %d, want %d", got, model.windowWidth)
	}
	if got := lipgloss.Height(view); got != model.windowHeight {
		t.Fatalf("Details height %d, want %d", got, model.windowHeight)
	}
}

func TestDetailsCanScrollBackAfterJumpingToEnd(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 72
	model.windowHeight = 12
	for line := 1; line <= 12; line++ {
		model.filteredValues[0].Locations = append(model.filteredValues[0].Locations, introspect.ValueUsageLocation{
			File:   fmt.Sprintf("/tmp/chart/templates/%02d.yaml", line),
			Line:   line,
			Column: 1,
			Kind:   "field",
		})
	}

	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	updated, _ = model.Update(key("b"))
	model = updated.(Model)
	updated, _ = model.Update(key("G"))
	model = updated.(Model)
	atEnd := model.View()
	updated, _ = model.Update(key("k"))
	model = updated.(Model)
	if afterUp := model.View(); afterUp == atEnd {
		t.Fatalf("expected k to scroll Details away from the end, still rendered:\n%s", afterUp)
	}
}

func TestValuesAlwaysConsolidateLocations(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 132
	model.windowHeight = 32
	component := model.chartNames[1]
	value := &model.components[component].Values[0]
	value.Locations = append(value.Locations, introspect.ValueUsageLocation{
		File: "/tmp/istiod/templates/service.yaml", Line: 20, Column: 3, Kind: "field",
	})
	model.updateFilteredValues()

	for _, shortcut := range []string{" "} {
		updated, _ := model.Update(key(shortcut))
		model = updated.(Model)
		if len(model.filteredValues) != 2 || len(model.filteredValues[0].Locations) != 2 {
			t.Fatalf("%q changed consolidated values: %+v", shortcut, model.filteredValues)
		}
		selected, ok := model.selectedValue()
		if !ok || len(model.inspectorSources(selected)) != 2 {
			t.Fatalf("%q removed a consolidated source occurrence", shortcut)
		}
	}
	if got := commandMatchCount("consolidated locations"); got != 0 {
		t.Fatalf("consolidation command remains available: %d matches", got)
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	openInspectorTestPanel(t, &model, detailSources)
	if len(model.detailItems) != 2 {
		t.Fatalf("Details should list both source locations, got %+v", model.detailItems)
	}
	for _, location := range []string{"deployment.yaml:148", "service.yaml:20"} {
		if !strings.Contains(model.View(), location) {
			t.Fatalf("Details did not render %s", location)
		}
	}
}

func TestPackageValuesWithoutChartsRemainSearchableAndShowProvenance(t *testing.T) {
	root := t.TempDir()
	firstSource := filepath.Join(root, "src", "my-package", "zarf-values.yaml")
	secondSource := filepath.Join(root, "zarf-values.yaml")
	writeModelTestSource(t, firstSource)
	writeModelTestSource(t, secondSource)
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{},
		PackageValues: []introspect.PackageValues{{
			PackageName: "api",
			Values:      map[string]interface{}{"database": map[string]interface{}{"host": "db.internal"}},
			Schema:      map[string]interface{}{"type": "object"},
			Files:       []string{"../../src/my-package/zarf-values.yaml", "zarf-values.yaml"},
			FileSources: []string{firstSource, secondSource},
			Usages: []introspect.PackageValueUsage{{
				Path: ".database.host", ComponentName: "server", ChartName: "app",
				TargetPath: "database.hostname", ExcludePaths: []string{"secret"}, Runtime: true,
			}},
		}},
	})
	model.selectSettingType(3)
	if len(model.filteredValues) != 2 || model.filteredValues[0].Path != "database" || model.filteredValues[0].settingChildren != 1 ||
		model.filteredValues[1].Path != "database.host" || model.filteredValues[1].settingParent != "database" {
		t.Fatalf("package leaf should appear once beneath its object without charts: %+v", model.filteredValues)
	}
	model.filterInput.SetValue("database.hostname")
	model.updateFilteredValues()
	if len(model.filteredValues) != 2 || !model.filteredValues[0].settingContext || model.filteredValues[1].Path != "database.host" || model.filteredValues[1].MappingStatus != "runtime" {
		t.Fatalf("mapping target should find its configured value: %+v", model.filteredValues)
	}
	model.cursor = 1
	model.focusPane = FocusInspector
	model.buildDetailItems()
	body, _ := model.inspectorContent(100, true)
	for _, want := range []string{"database.host", "db.internal", "Inspected input"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in package summary: %s", want, body)
		}
	}
	if strings.Contains(body, "EFFECTIVE VALUE") || strings.Contains(body, "Helm .Values") {
		t.Fatalf("runtime use cannot show an effective Helm mapping: %s", body)
	}
	openInspectorTestPanel(t, &model, detailReferences)
	body, _ = model.inspectorContent(100, true)
	for _, want := range []string{"runtime", "server/app", "database.hostname"} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("missing observed runtime reference %q: %s", want, body)
		}
	}
	updated, _ := model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	model.filterInput.SetValue("zarf-values.yaml")
	model.updateFilteredValues()
	if len(model.filteredValues) != 2 || model.filteredValues[1].Path != "database.host" {
		t.Fatalf("file provenance should remain searchable: %+v", model.filteredValues)
	}
	model.cursor = 1
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	body, _ = model.inspectorContent(100, true)
	if strings.Count(body, "Sources (2)") != 1 || strings.Contains(body, root) ||
		!strings.Contains(body, "Schema") || strings.Contains(body, "type: object") || strings.Contains(body, "../..") {
		t.Fatalf("summary should offer compact source and separate schema links: %s", body)
	}
	openInspectorTestPanel(t, &model, detailSources)
	if len(model.detailItems) != 2 || model.detailItems[0].Kind != DetailItemSource ||
		model.detailItems[0].File != firstSource || model.detailItems[1].File != secondSource {
		t.Fatalf("values files should be selectable in declared order: %+v", model.detailItems)
	}
	if model.detailItems[0].Label == model.detailItems[1].Label || strings.Contains(model.detailItems[0].Label, root) || strings.Contains(model.detailItems[1].Label, root) {
		t.Fatalf("duplicate filenames need distinct compact labels without cache roots: %+v", model.detailItems)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	if len(model.detailItems) != 1 || model.detailItems[0].Kind != DetailItemFile || model.detailItems[0].File != firstSource {
		t.Fatalf("source details lost the original file target: %+v", model.detailItems)
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("s"))
	model = updated.(Model)
	body, _ = model.inspectorContent(100, true)
	if model.detailPanel != detailSchema || !strings.Contains(body, "type: object") || !strings.Contains(body, "annotations, not validation") {
		t.Fatalf("schema should open as an annotation page: %s", body)
	}
}

func TestDefaultValueStyleHasNoBackground(t *testing.T) {
	if _, ok := detailDefaultStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("default values should not have a background color")
	}
}

func TestMappingChartFirstRootAndSubtree(t *testing.T) {
	root := introspect.PackageValueUsage{Path: ".istio-controlplane.base", TargetPath: ".", ComponentName: "istio-controlplane", ChartName: "base",
		ExcludePaths: []string{".istio-controlplane.base._internal_defaults_do_not_set", ".istio-controlplane.base.global.hub"}}
	subtree := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"core/istio-controlplane/base": {Values: []introspect.ValueUsage{
				{Path: "base.enableCRDTemplates", DefaultValue: false}, {Path: "global.hub", DefaultValue: "docker.io"},
				{Path: "_internal_defaults_do_not_set.flag", DefaultValue: true},
			}},
			"core/server/web": {Values: []introspect.ValueUsage{
				{Path: "config.replicas", DefaultValue: 1}, {Path: "config.enabled", DefaultValue: true},
				{Path: "other.path", DefaultValue: "outside"},
			}},
		},
		PackageValues: []introspect.PackageValues{{PackageName: "core",
			Values: map[string]interface{}{"istio-controlplane": map[string]interface{}{"base": map[string]interface{}{}},
				"app": map[string]interface{}{"replicas": 0, "enabled": false, "undiscovered": "supplied"}},
			Files: []string{"packaged.yaml", "operator.yaml"}, Usages: []introspect.PackageValueUsage{root, subtree},
		}},
	})
	for i, item := range model.filteredValues {
		if item.IsPackageValue && item.Path == "istio-controlplane.base" {
			model.cursor = i
			break
		}
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if model.focusPane != FocusInspector {
		t.Fatal("package input should first open its compact summary")
	}
	openInspectorTestMapping(t, &model)
	if model.focusPane != FocusMapping || model.mapping.inside != 3 {
		t.Fatalf("empty source should expose three chart paths: %+v", model.mapping)
	}
	rows := map[string]mappingRow{}
	for _, row := range model.mapping.rows {
		rows[row.path] = row
	}
	if got := rows["base.enableCRDTemplates"]; got.status != "available" || got.present || got.defaultValue != false ||
		got.packagePath != ".istio-controlplane.base.base.enableCRDTemplates" {
		t.Fatalf("chart candidate with absent input: %+v", got)
	}
	if got := rows["global.hub"]; got.status != "excluded" || got.exclusion != ".istio-controlplane.base.global.hub" {
		t.Fatalf("fully prefixed exclusion: %+v", got)
	}
	if rows["_internal_defaults_do_not_set.flag"].status != "excluded" {
		t.Fatalf("excluded descendant: %+v", rows)
	}
	if strings.Contains(model.View(), "No configured paths") {
		t.Fatal("empty object must not imply empty chart")
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	model.selectSettingType(4)
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.MappingChart == "web" {
			model.cursor = i
			break
		}
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	openInspectorTestMapping(t, &model)
	if model.mapping.inside != 3 {
		t.Fatalf("two discovered subtree paths and one supplied-only path: %+v", model.mapping.rows)
	}
	rows = map[string]mappingRow{}
	for _, row := range model.mapping.rows {
		rows[row.path] = row
	}
	if got := rows["config.replicas"]; !got.present || got.input != 0 || got.defaultValue != 1 {
		t.Fatalf("zero input vs chart default: %+v", got)
	}
	if got := rows["config.enabled"]; !got.present || got.input != false {
		t.Fatalf("false input: %+v", got)
	}
	if !rows["config.undiscovered"].undiscovered || !rows["config.undiscovered"].present {
		t.Fatalf("supplied input absent from discovery: %+v", rows)
	}
	updated, _ = model.Update(key("a"))
	model = updated.(Model)
	if len(model.mapping.rows) != 4 || model.mapping.rows[3].path != "other.path" || model.mapping.rows[3].status != "other" {
		t.Fatalf("toggle reveals outside destination chart paths only: %+v", model.mapping.rows)
	}
	updated, _ = model.Update(key("G"))
	model = updated.(Model)
	model.windowWidth, model.windowHeight = 72, 14
	model.ensureMappingCursorVisible()
	if view := model.View(); !strings.Contains(view, "other.path") || !strings.Contains(view, "other") || lipgloss.Width(view) != 72 {
		t.Fatalf("narrow table should keep status and selected row visible: %s", view)
	}
	model.windowHeight = 28
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("a"))
	model = updated.(Model)
	if model.mapping.cursor >= len(model.mapping.rows) || model.mapping.rows[model.mapping.cursor].status == "other" {
		t.Fatalf("hiding other rows should retain a valid selection: %+v", model.mapping)
	}
}

func TestMappingPackageOwnershipAndMissingSource(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".missing", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"b/server/web": {Values: []introspect.ValueUsage{{Path: "config.enabled", DefaultValue: true}},
				PackageValueMappings: []introspect.PackageValueUsage{usage}},
		},
		ComponentOrder: []string{"b/server/web"},
		PackageValues: []introspect.PackageValues{
			{PackageName: "a", Values: map[string]interface{}{"missing": map[string]interface{}{"enabled": false}}, Usages: []introspect.PackageValueUsage{usage}},
			{PackageName: "b", Values: map[string]interface{}{}, Usages: []introspect.PackageValueUsage{usage}},
		},
	})
	model.selectSettingType(4)
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.PackageName == "b" {
			model.cursor = i
			break
		}
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	openInspectorTestMapping(t, &model)
	if model.mapping == nil || model.mapping.pkg.PackageName != "b" || model.mapping.inside != 1 || model.mapping.rows[0].present {
		t.Fatalf("missing-source declaration must reveal chart candidates owned by b: %+v", model.mapping)
	}
	if model.mapping.rows[0].packagePath != ".missing.enabled" {
		t.Fatalf("missing input lost its package coordinate: %+v", model.mapping.rows[0])
	}
	for _, want := range []string{"No setting input", "config.enabled"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("live Inspector lost missing-input evidence %q: %s", want, model.View())
		}
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	pressWorkspaceKey(&model, "esc")
	pressWorkspaceKey(&model, "esc")
	model.currentChart = "b/server/web"
	model.updateFilteredValues()
	for i, item := range model.filteredValues {
		if item.IsPackageMapping {
			model.cursor = i
			break
		}
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	openInspectorTestMapping(t, &model)
	if model.mapping == nil || model.mapping.pkg.PackageName != "b" {
		t.Fatalf("chart mapping must resolve owning package: %+v", model.mapping)
	}
}

func TestPackageValuesKeepPackageBandsDistinct(t *testing.T) {
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{},
		PackageValues: []introspect.PackageValues{
			{PackageName: "api", Values: map[string]interface{}{"port": 80}},
			{PackageName: "worker", Values: map[string]interface{}{"port": 81}},
		},
	})
	model.windowWidth, model.windowHeight = 140, 22
	view := model.valuesPane(140, 20)
	if !strings.Contains(view, "━━ api ━") || !strings.Contains(view, "━━ worker ━") {
		t.Fatalf("each package must retain its own heading:\n%s", view)
	}
	for _, item := range model.filteredValues {
		if origin, pending := model.settingPackageOrigin(item); origin != "unknown" || pending {
			t.Fatalf("package source provenance must remain unknown without snapshots: %+v", item)
		}
	}
}

func TestNextBundleMetadataNeverSuggestsLegacyAuthoring(t *testing.T) {
	const chart = "api/server/app"
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{chart: {
			Values: []introspect.ValueUsage{{Path: "image.tag", DefaultValue: "1"}},
		}},
		ComponentOrder: []string{chart},
		Bundle: &introspect.BundleMetadata{
			Format: "next", Name: "platform", Version: "2",
			Variables: map[string]interface{}{"replicas": 3},
			Packages: []introspect.BundlePackageMetadata{{
				ID: "api", Source: "oci://registry/api", ValuesFiles: []string{"values/api/0.yaml"},
				DependsOn: []string{"base"}, Namespace: "team", OptionalComponents: []string{"server"},
			}},
		},
	})
	if !model.nextRail || model.allCategories || model.category != 0 || len(model.filteredValues) != 1 || !model.filteredValues[0].IsBundleVariable || model.categoryCounts[0] != 1 ||
		model.categoryCounts[1] != 1 || model.categoryCounts[5] != 1 {
		t.Fatalf("Next metadata and Helm defaults must have separate categories: %+v", model.categoryCounts)
	}
	model.selectNextCategory(1)
	model.filterInput.SetValue("values/api/0.yaml")
	model.updateFilteredValues()
	model.buildDetailItems()
	if len(model.filteredValues) != 1 || model.filteredValues[0].Path != "api" {
		t.Fatalf("values file provenance should remain searchable: %+v", model.filteredValues)
	}
	model.focusPane = FocusInspector
	openInspectorTestPanel(t, &model, detailReferences)
	body, _ := model.inspectorContent(100, true)
	for _, want := range []string{"platform 2", "oci://registry/api", "Bundle values file 1", "base", "team", "server"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in Next package details: %s", want, body)
		}
	}
	if len(model.detailItems) != 1 || model.detailItems[0].Kind != DetailItemSource || model.detailItems[0].File != "values/api/0.yaml" {
		t.Fatalf("Next values source lost its exact file target: %+v", model.detailItems)
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	body, _ = model.inspectorContent(100, true)
	if !strings.Contains(body, filepath.FromSlash("values/api/0.yaml")) {
		t.Fatalf("source details lost Next values file: %s", body)
	}
	model.detailBundleMode = true
	if snippet := model.generateBundleSnippetForItem(DisplayItem{Component: chart, ValueUsage: introspect.ValueUsage{Path: "image.tag"}}); snippet != "" {
		t.Fatalf("Next chart must not suggest legacy uds-bundle.yaml: %s", snippet)
	}
}

func TestNextCategoryFiltersKeepConfigurationOwnersSeparate(t *testing.T) {
	const chart = "api/server/app"
	m := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{chart: {
			Values:               []introspect.ValueUsage{{Path: "image.tag", DefaultValue: "1"}},
			ZarfVars:             []introspect.ZarfVariableUsage{{Name: "REGISTRY"}},
			UDSOverrideValues:    []introspect.UDSOverrideValue{{Path: "replicas", Value: 3}},
			PackageValueMappings: []introspect.PackageValueUsage{{Path: ".image.tag", TargetPath: "image.tag", ChartName: "app"}},
		}},
		ComponentOrder: []string{chart},
		PackageValues: []introspect.PackageValues{{
			PackageName: "api", Values: map[string]interface{}{"image": map[string]interface{}{"tag": "2"}},
			Usages: []introspect.PackageValueUsage{{Path: ".image.tag", TargetPath: "image.tag", ChartName: "app"}},
		}},
		Bundle: &introspect.BundleMetadata{
			Format: "next", Name: "platform",
			Variables: map[string]interface{}{"cluster": "dev"},
			Packages:  []introspect.BundlePackageMetadata{{ID: "api", ValuesFiles: []string{"values/api/0.yaml"}}},
		},
	})
	if !m.nextRail || m.categoryCounts != [7]int{1, 1, 1, 2, 1, 1, 1} {
		t.Fatalf("configuration owners should each have a visible category: %+v", m.categoryCounts)
	}
	for _, tc := range []struct {
		key   string
		paths []string
	}{
		{"1", []string{"cluster"}}, {"2", []string{"api"}}, {"3", []string{"REGISTRY"}}, {"4", []string{"image", "image.tag"}},
		{"5", []string{".image.tag"}}, {"6", []string{"image.tag"}}, {"7", []string{"replicas"}},
	} {
		updated, _ := m.Update(key(tc.key))
		m = updated.(Model)
		var paths []string
		for _, item := range m.filteredValues {
			paths = append(paths, item.Path)
			if nextCategory(item) != int(tc.key[0]-'1') {
				t.Fatalf("category %s mixed input kinds: %+v", tc.key, m.filteredValues)
			}
		}
		if !reflect.DeepEqual(paths, tc.paths) {
			t.Fatalf("category %s mixed input kinds: %+v", tc.key, m.filteredValues)
		}
		if tc.key == "4" && (m.filteredValues[0].settingChildren != 1 || m.filteredValues[1].settingParent != "image") {
			t.Fatalf("package inputs lost their object hierarchy: %+v", m.filteredValues)
		}
	}
	updated, _ := m.Update(key("6"))
	m = updated.(Model)
	updated, _ = m.Update(key("enter"))
	m = updated.(Model)
	if m.focusPane != FocusInspector || !strings.Contains(m.View(), "image.tag") {
		t.Fatal("chart value must still open Details")
	}
	updated, _ = m.Update(key("esc"))
	m = updated.(Model)
	if m.focusPane != FocusValues || m.category != 5 || m.filteredValues[0].Path != "image.tag" {
		t.Fatal("Details must return to the selected category")
	}
}

func TestNextCategoryFilterRemainsIndependentOfScope(t *testing.T) {
	m := NewModel(introspect.InspectionReport{
		Components:     map[string]*introspect.IntrospectResult{"api/server/app": {Values: []introspect.ValueUsage{{Path: "image.tag"}}}},
		ComponentOrder: []string{"api/server/app"},
		Bundle:         &introspect.BundleMetadata{Format: "next", Variables: map[string]interface{}{"cluster": "dev"}},
	})
	for _, width := range []int{100, 72} {
		resizeWorkspace(&m, width, 30)
		view := assertWorkspaceSize(t, m, width, 30)
		if !strings.Contains(view, "Settings") || !strings.Contains(view, "1 Bundle vars") || !strings.Contains(view, "8 All") || strings.Contains(view, "CATEGORIES") {
			t.Fatalf("category choices should be a Settings filter at %d columns:\n%s", width, view)
		}
	}
	pressWorkspaceKey(&m, "6")
	if m.focusPane != FocusValues || len(m.filteredValues) != 1 || m.filteredValues[0].Path != "image.tag" {
		t.Fatal("digit shortcut should filter the Settings rows")
	}
	m.currentChart = "api/server/app"
	m.updateFilteredValues()
	m.selectNextCategory(0)
	if len(m.filteredValues) != 0 || !strings.Contains(m.View(), "No Bundle vars in this scope") {
		t.Fatal("chart scope must not leak bundle-wide inputs through the category filter")
	}
}

func TestNextWorkspaceStartsWithBundleFilesAndAllExposesEveryType(t *testing.T) {
	m := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"api/server/app": {Values: []introspect.ValueUsage{{Path: "enabled"}}}},
		Bundle: &introspect.BundleMetadata{
			Format: "next", Packages: []introspect.BundlePackageMetadata{{ID: "api", ValuesFiles: []string{"values.yaml"}}},
		},
	})
	if m.allCategories || m.category != 1 || len(m.filteredValues) != 1 || m.filteredValues[0].Path != "api" || m.filteredValues[0].Component != "UDS Next bundle" {
		t.Fatalf("initial Settings should select the available bundle file type: %+v", m.filteredValues)
	}
	pressWorkspaceKey(&m, "8")
	if !m.allCategories || len(m.filteredValues) != 2 {
		t.Fatalf("All should include both bundle files and Helm values: %+v", m.filteredValues)
	}
	found := map[string]bool{}
	for _, item := range m.filteredValues {
		found[item.Path] = true
	}
	if !found["api"] || !found["enabled"] {
		t.Fatalf("All Settings omitted a configuration owner: %+v", m.filteredValues)
	}
}

func TestNextZarfVariablesGroupByOwningPackage(t *testing.T) {
	serverSource, workerSource := t.TempDir(), t.TempDir()
	m := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"api/server/app": {Source: serverSource, ZarfVars: []introspect.ZarfVariableUsage{{
				Name: "ADMIN_DOMAINS", Locations: []introspect.ValueUsageLocation{{File: filepath.Join(serverSource, "deployment.yaml"), Line: 5, Column: 1}},
			}}},
			"api/worker/app": {Source: workerSource, ZarfVars: []introspect.ZarfVariableUsage{{
				Name: "ADMIN_DOMAINS", Locations: []introspect.ValueUsageLocation{{File: filepath.Join(workerSource, "worker.yaml"), Line: 9, Column: 2}},
			}}},
			"api.v2/server/app": {ZarfVars: []introspect.ZarfVariableUsage{{Name: "ADMIN_DOMAINS"}}},
		},
		ComponentOrder: []string{"api/server/app", "api/worker/app", "api.v2/server/app"},
		Variables: []v1alpha1.InteractiveVariable{
			{Variable: v1alpha1.Variable{Name: "api.ADMIN_DOMAINS"}, Default: "api.example", Description: "Admin domains"},
			{Variable: v1alpha1.Variable{Name: "api.v2.ADMIN_DOMAINS"}, Default: "v2.example"},
		},
		Bundle: &introspect.BundleMetadata{
			Format: "next", Packages: []introspect.BundlePackageMetadata{{ID: "api"}, {ID: "api.v2"}},
		},
	})
	m.windowWidth, m.windowHeight = 100, 30
	m.selectNextCategory(2)
	if len(m.filteredValues) != 2 {
		t.Fatalf("one selectable ADMIN_DOMAINS per package, got %+v", m.filteredValues)
	}
	first, second := m.filteredValues[0], m.filteredValues[1]
	if first.PackageName != "api" || first.Path != "api.ADMIN_DOMAINS" ||
		first.DefaultValue != "api.example" || first.PkgVarDesc != "Admin domains" ||
		first.ZarfLocationCount != 2 || len(first.ZarfReferences) != 2 ||
		second.PackageName != "api.v2" || second.Path != "api.v2.ADMIN_DOMAINS" || second.DefaultValue != "v2.example" {
		t.Fatalf("grouping must retain package identity, declaration and both chart references: %+v", m.filteredValues)
	}
	view := m.valuesPane(100, 28)
	for _, band := range []string{"━━ api ━", "━━ api.v2 ━"} {
		if strings.Count(view, band) != 1 {
			t.Fatalf("expected exactly one package section %q:\n%s", band, view)
		}
	}
	if strings.Contains(view, "api.ADMIN_DOMAINS") || strings.Count(view, "ADMIN_DOMAINS") != 2 {
		t.Fatalf("the column should show one local name per package:\n%s", view)
	}
	updated, _ := m.Update(key("enter"))
	detailModel := updated.(Model)
	details := detailModel.View()
	for _, want := range []string{"api.ADMIN_DOMAINS", "Admin domains", "Sources (2)"} {
		if !strings.Contains(details, want) {
			t.Fatalf("variable Details lost %q:\n%s", want, details)
		}
	}
	openInspectorTestPanel(t, &detailModel, detailSources)
	if len(detailModel.detailItems) != 2 {
		t.Fatalf("grouped variable lost sources: %+v", detailModel.detailItems)
	}
	for index, want := range []struct {
		component, file string
		line, column    int
	}{
		{"api/server/app", filepath.Join(serverSource, "deployment.yaml"), 5, 1},
		{"api/worker/app", filepath.Join(workerSource, "worker.yaml"), 9, 2},
	} {
		source := detailModel.detailItems[index]
		if !strings.Contains(source.Label, strings.TrimPrefix(want.component, "api/")) {
			t.Fatalf("source label lost its owning chart: %q", source.Label)
		}
		if source.sourceComponent != want.component || source.File != want.file || source.Location.Line != want.line || source.Location.Column != want.column {
			t.Fatalf("grouped source lost owning chart or editor location: %+v", source)
		}
	}
	m.filterInput.SetValue("api.v2")
	m.updateFilteredValues()
	if len(m.filteredValues) != 1 || m.filteredValues[0].Path != "api.v2.ADMIN_DOMAINS" {
		t.Fatalf("searching a package must retain only its variable: %+v", m.filteredValues)
	}
	m.filterInput.SetValue("worker/app")
	m.updateFilteredValues()
	if len(m.filteredValues) != 1 || m.filteredValues[0].Path != "api.ADMIN_DOMAINS" ||
		m.filteredValues[0].DefaultValue != "api.example" || m.filteredValues[0].ZarfLocationCount != 1 {
		t.Fatalf("chart-only search must retain declaration and matching chart reference: %+v", m.filteredValues)
	}
}

func testWorkspaceModel() Model {
	const component = "core-base/istio-controlplane/istiod"
	source := filepath.Join(os.TempDir(), "istiod")
	return NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{

			component: {
				ChartName:    "istiod",
				ChartVersion: "1.27.1",
				Source:       source,
				Values: []introspect.ValueUsage{
					{
						Path:         "global.proxy.resources.requests.memory",
						Chart:        "istiod",
						DefaultValue: "128Mi",
						Locations: []introspect.ValueUsageLocation{
							{File: filepath.Join(source, "templates", "deployment.yaml"), Line: 148, Column: 12, Kind: "field"},
						},
					},
					{Path: "pilot.autoscaleEnabled", Chart: "istiod", DefaultValue: true},
				},
			},
		},
		ComponentOrder: []string{component},
	})
}

func writeModelTestSource(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# source fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func openInspectorTestMapping(t *testing.T, model *Model) {
	t.Helper()
	if model.focusPane != FocusInspector {
		t.Fatalf("mapping declaration must first focus Inspector, got %v", model.focusPane)
	}
	if model.detailPanel != detailMappingRules {
		openInspectorTestPanel(t, model, detailMappingRules)
	}
	for index, item := range model.detailItems {
		if item.Kind != DetailItemMapping {
			continue
		}
		model.detailCursor = index
		pressWorkspaceKey(model, "enter")
		if model.mapping == nil || model.focusPane != FocusMapping {
			t.Fatal("mapping action did not open the Settings table")
		}
		return
	}
	t.Fatalf("Inspector offers no mapping action: %+v", model.detailItems)
}

func openInspectorTestPanel(t *testing.T, model *Model, panel detailPanel) {
	t.Helper()
	for index, item := range model.detailItems {
		if item.Kind != DetailItemPanel || item.panel != panel {
			continue
		}
		model.detailCursor = index
		updated, _ := model.Update(key("enter"))
		*model = updated.(Model)
		if model.detailPanel != panel || model.focusPane != FocusInspector {
			t.Fatalf("panel %v action did not open its page", panel)
		}
		return
	}
	if panel == detailSources {
		pressWorkspaceKey(model, "o")
		if model.detailPanel != detailSources || model.focusPane != FocusInspector {
			t.Fatal("o shortcut did not open the complete source list")
		}
		return
	}
	t.Fatalf("panel %v has no selectable action: %+v", panel, model.detailItems)
}

func key(value string) tea.KeyMsg {
	switch value {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
	}
}

func TestChartDetailsShareMappingContextAndRestoreEntryPoints(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	excluded := usage
	excluded.Path = ".fallback"
	excluded.ExcludePaths = []string{".fallback.enabled"}
	location := introspect.ValueUsageLocation{File: "/charts/web/templates/deployment.yaml", Line: 12, Column: 8}
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"b/server/web": {ChartName: "web", Source: "/charts/web", Values: []introspect.ValueUsage{
				{Path: "config.enabled", DefaultValue: true, Locations: []introspect.ValueUsageLocation{location}},
			}},
		},
		ComponentOrder: []string{"b/server/web"},
		PackageValues: []introspect.PackageValues{
			{PackageName: "a", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": "foreign"}}, Usages: []introspect.PackageValueUsage{usage}},
			{PackageName: "b", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}, "fallback": map[string]interface{}{"enabled": nil}},
				Files: []string{"packaged.yaml", "operator.yaml"}, Usages: []introspect.PackageValueUsage{usage, excluded}},
		},
	})
	model.windowWidth, model.windowHeight = 120, 36
	model.selectSettingType(5)
	for i, item := range model.filteredValues {
		if item.Component == "b/server/web" && item.Path == "config.enabled" && !item.IsPackageValue && !item.IsPackageMapping {
			model.cursor = i
			break
		}
	}
	originCursor := model.cursor
	press := func(k string) {
		t.Helper()
		updated, _ := model.Update(key(k))
		model = updated.(Model)
	}
	press("enter")
	if len(model.detailItems) != 3 || model.detailItems[0].Kind != DetailItemPanel || model.detailItems[0].panel != detailAffected ||
		model.detailItems[1].Kind != DetailItemPanel || model.detailItems[1].panel != detailMappingRules ||
		model.detailItems[2].Kind != DetailItemPanel || model.detailItems[2].panel != detailSources {
		t.Fatalf("want underlying values first, mapping explanation, and a compact Sources action: %+v", model.detailItems)
	}
	direct, _ := model.inspectorContent(116, true)
	openInspectorTestPanel(t, &model, detailMappingRules)
	if len(model.detailItems) != 2 || model.detailItems[0].Kind != DetailItemMapping || model.detailItems[1].Kind != DetailItemMapping {
		t.Fatalf("mapping explanation must retain both declaration occurrences: %+v", model.detailItems)
	}
	selected, _ := model.detailValue()
	details := model.valuePolicy.details(selected.Component, selected.ValueUsage)
	if len(details) != 2 || model.detailItems[0].mappingRef != details[0].mapping || model.detailItems[1].mappingRef != details[1].mapping {
		t.Fatalf("explanation changed mapping identity or declaration order: %+v", model.detailItems)
	}
	first, second := details[0], details[1]
	if first.pkg.PackageName != "b" || !first.row.present || first.row.input != false ||
		first.row.packagePath != ".app.enabled" || second.row.status != "excluded" ||
		!second.row.present || second.row.input != nil || second.row.exclusion != ".fallback.enabled" {
		t.Fatalf("mapping precedence/provenance lost: %+v %+v", first, second)
	}
	if !reflect.DeepEqual(first.pkg.Files, []string{"packaged.yaml", "operator.yaml"}) ||
		len(model.inspectorSources(model.filteredValues[model.cursor])) != 1 || model.inspectorSources(model.filteredValues[model.cursor])[0].Location != location {
		t.Fatal("ordered inputs or editor location lost")
	}
	model.detailCursor = 0
	press("enter")
	if model.focusPane != FocusMapping {
		t.Fatal("chart Details mapping link did not open the chart-first table")
	}
	table := model.mapping
	press("enter")
	viaMapping, _ := model.inspectorContent(116, true)
	if direct != viaMapping {
		t.Fatalf("same chart value differs by entry point:\nDIRECT:\n%s\nMAPPING:\n%s", direct, viaMapping)
	}
	openInspectorTestPanel(t, &model, detailMappingRules)
	model.detailCursor = 0
	press("j")
	if model.detailCursor != 1 {
		t.Fatal("mapping-origin Details cannot select another mapping")
	}
	press("enter")
	if model.focusPane != FocusMapping || model.mapping.usage.Path != ".fallback" {
		t.Fatal("alternative mapping link did not open its declaration")
	}
	press("esc")
	if model.focusPane != FocusInspector || !model.mappingRowDetail || model.mapping != table || model.detailCursor != 1 || model.detailPanel != detailMappingRules {
		t.Fatal("nested mapping did not restore originating Details selection")
	}
	press("esc")
	if model.detailPanel != detailSummary || model.detailCursor != 1 {
		t.Fatal("mapping explanation did not restore its summary action")
	}
	press("o")
	if model.detailPanel != detailSources || len(model.detailItems) != 1 || model.detailItems[0].Location != location {
		t.Fatal("mapping-origin Details cannot reach the template source")
	}
	press("esc")
	if model.detailPanel != detailSummary || model.detailCursor != 1 {
		t.Fatal("source list did not restore its originating summary selection")
	}
	press("esc")
	if model.focusPane != FocusMapping || model.mapping != table {
		t.Fatal("mapping-origin Details did not return to its table")
	}
	press("esc")
	if model.focusPane != FocusInspector || model.mapping != nil || model.mappingRowDetail || model.cursor != originCursor || model.detailPanel != detailMappingRules {
		t.Fatal("table did not restore the main chart mapping explanation")
	}
	press("esc")
	if model.detailPanel != detailSummary || model.detailCursor != 1 {
		t.Fatal("mapping explanation did not restore the main chart summary")
	}
	press("esc")
	if model.focusPane != FocusValues || model.cursor != originCursor {
		t.Fatal("main chart Details did not preserve workspace selection")
	}
	model.selectSettingType(4)
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.PackageName == "b" && item.ValueSource == ".app" {
			model.cursor = i
			break
		}
	}
	packageCursor := model.cursor
	press("enter")
	openInspectorTestMapping(t, &model)
	press("enter")
	fromPackage, _ := model.inspectorContent(116, true)
	if fromPackage != direct {
		t.Fatalf("package entry lost common chart Details:\n%s", fromPackage)
	}
	press("esc")
	press("esc")
	press("esc")
	press("esc")
	if model.focusPane != FocusValues || model.cursor != packageCursor {
		t.Fatal("package entry did not preserve workspace selection")
	}
}

func TestMappingDetailsKeepRelatedValuesOutsideWorkspaceFilter(t *testing.T) {
	const component = "core/server/web"
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{component: {
			Values: []introspect.ValueUsage{{Path: "config.enabled", DefaultValue: true,
				Locations: []introspect.ValueUsageLocation{{File: "/web/templates/config.yaml", Line: 3, Column: 4}}}},
			UDSOverrideValues:    []introspect.UDSOverrideValue{{Path: "config.enabled", Value: false, OriginalDefault: true}},
			UDSOverrideVariables: []introspect.UDSOverrideVariable{{Name: "ENABLED", Path: "config.enabled", Default: true, Description: "Enable service"}},
		}},
		ComponentOrder: []string{component},
		PackageValues: []introspect.PackageValues{{PackageName: "core",
			Values: map[string]interface{}{"app": map[string]interface{}{}},
			Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}},
		}},
	})
	press := func(k string) {
		t.Helper()
		updated, _ := model.Update(key(k))
		model = updated.(Model)
	}
	model.selectSettingType(5)
	model.filterInput.SetValue("config.enabled")
	model.updateFilteredValues()
	for i, value := range model.filteredValues {
		if value.Path == "config.enabled" && !value.IsUDSOverrideValue && !value.IsUDSOverrideVar {
			model.cursor = i
			break
		}
	}
	press("enter")
	direct, _ := model.inspectorContent(116, true)
	press("esc")
	model.selectSettingType(3)
	model.filterInput.SetValue("app")
	model.updateFilteredValues()
	model.cursor = 0
	for _, value := range model.filteredValues {
		if value.IsUDSOverrideValue || value.IsUDSOverrideVar {
			t.Fatal("fixture must hide related values from workspace")
		}
	}
	press("enter")
	openInspectorTestMapping(t, &model)
	press("enter")
	mapped, _ := model.inspectorContent(116, true)
	if mapped != direct || len(model.detailItems) != 5 {
		t.Fatalf("filter changed chart Details or hid related values:\n%s", mapped)
	}
	press("G")
	press("enter")
	value, ok := model.detailValue()
	if !ok || !value.IsUDSOverrideVar || value.Path != "ENABLED" || value.UDSVarDescription != "Enable service" {
		t.Fatalf("hidden variable target lost: %+v", value)
	}
	press("G")
	press("enter")
	value, ok = model.detailValue()
	if !ok || value.IsUDSOverrideVar || value.Path != "config.enabled" || value.DefaultValue != true {
		t.Fatalf("hidden chart backlink lost: %+v", value)
	}
	press("esc")
	value, ok = model.detailValue()
	if !ok || !value.IsUDSOverrideVar || value.Path != "ENABLED" {
		t.Fatal("related backlink did not preserve the preceding variable in its return chain")
	}
	press("esc")
	value, ok = model.detailValue()
	if !ok || value.Path != "config.enabled" || value.IsUDSOverrideVar {
		t.Fatal("related variable did not return to its originating chart value")
	}
	press("esc")
	if model.focusPane != FocusMapping || model.filterInput.Value() != "app" || model.cursor != 0 {
		t.Fatal("related navigation changed the mapping origin or workspace filter")
	}
}
