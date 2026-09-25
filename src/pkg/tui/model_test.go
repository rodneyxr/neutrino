package tui

import (
	"fmt"
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
		name        string
		width       int
		height      int
		want        []string
		doesNotWant []string
	}{
		{
			name:        "wide",
			width:       132,
			height:      32,
			want:        []string{"COMPONENTS", "VALUES", "2 panes"},
			doesNotWant: []string{"DETAILS"},
		},
		{
			name:        "medium",
			width:       100,
			height:      28,
			want:        []string{"COMPONENTS", "VALUES", "2 panes"},
			doesNotWant: []string{"DETAILS"},
		},
		{
			name:        "compact",
			width:       72,
			height:      24,
			want:        []string{"VALUES", "single pane"},
			doesNotWant: []string{"COMPONENTS", "DETAILS"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := testWorkspaceModel()
			updated, _ := model.Update(tea.WindowSizeMsg{Width: test.width, Height: test.height})
			view := updated.(Model).View()

			if got := lipgloss.Width(view); got != test.width {
				t.Fatalf("rendered width %d, want %d", got, test.width)
			}
			if got := lipgloss.Height(view); got != test.height {
				t.Fatalf("rendered height %d, want %d", got, test.height)
			}
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
		model.cursor = 9
		model.ensureCursorVisible()
		if model.viewportOffset != 0 {
			t.Fatalf("width %d: cursor fits on last row without scrolling; viewport offset = %d", width, model.viewportOffset)
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

func TestKeyboardFlowOpensAndClosesFullWidthDetails(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 132
	model.windowHeight = 32

	updated, _ := model.Update(key("enter"))
	details := updated.(Model)
	view := details.View()
	if !strings.Contains(view, "DETAILS") {
		t.Fatalf("enter did not open full-width details:\n%s", view)
	}
	if strings.Contains(view, "COMPONENTS") {
		t.Fatal("details should replace the two-pane workspace")
	}

	updated, _ = details.Update(key("esc"))
	workspace := updated.(Model).View()
	if !strings.Contains(workspace, "VALUES") || !strings.Contains(workspace, "COMPONENTS") {
		t.Fatalf("escape did not restore the two-pane workspace:\n%s", workspace)
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
		!strings.Contains(model.workspaceFooter(100), "tab/arrows panes") {
		t.Fatalf("status did not return to default footer: %q", model.workspaceFooter(100))
	}
}

func TestNewStatusRefreshesExpiry(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100

	updated, _ := model.Update(key("Z"))
	model = updated.(Model)
	oldExpiry := model.statusExpiresAt
	updated, _ = model.Update(key("U"))
	model = updated.(Model)
	updated, _ = model.Update(pathScrollTickMsg(oldExpiry))
	model = updated.(Model)
	if !strings.Contains(model.workspaceFooter(100), "Bundle override summary on") {
		t.Fatalf("previous message expired the newer status: %q", model.workspaceFooter(100))
	}
}

func TestValuesAreGroupedByComponentAndSection(t *testing.T) {
	const first = "core-base/istio-controlplane/istiod"
	const second = "core-base/istio-admin-gateway/gateway"
	model := Model{
		components: map[string]*introspect.IntrospectResult{
			first:  {ChartName: "istiod"},
			second: {ChartName: "gateway"},
		},
		chartNames:   []string{"All Charts", first, second},
		currentChart: "All Charts",
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

	view := model.View()
	headings := []string{
		"━━ Zarf package",
		"  " + valueSection(model.filteredValues[0]),
		"━━ " + first,
		"  " + valueSection(model.filteredValues[2]),
		"  " + valueSection(model.filteredValues[3]),
		"━━ " + second,
		"  " + valueSection(model.filteredValues[4]),
	}
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
	if strings.Count(view, "━━ "+first) != 1 {
		t.Fatalf("component band must appear once across its sections:\n%s", view)
	}
	if got := model.valueLineCount(1, 2); got != 6 {
		t.Fatalf("two sections within a component should have a blank separator: got %d lines, want 6", got)
	}
	model.viewportOffset = 2
	scrolled := model.View()
	if !strings.Contains(scrolled, "━━ "+first) || !strings.Contains(scrolled, "  "+valueSection(model.filteredValues[2])) {
		t.Fatalf("scrolling into a component must retain its context:\n%s", scrolled)
	}
}

func TestLeftColumnExpandsWhenFocused(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 132
	model.windowHeight = 32

	model.focusPane = FocusValues
	unfocusedWidth := model.componentWidth(model.windowWidth)
	if unfocusedWidth != 24 {
		t.Fatalf("expected unfocused component width 24, got %d", unfocusedWidth)
	}

	model.focusPane = FocusComponents
	focusedWidth := model.componentWidth(model.windowWidth)
	if focusedWidth <= unfocusedWidth {
		t.Fatalf("expected focused component width to expand beyond %d, got %d", unfocusedWidth, focusedWidth)
	}
	if focusedWidth != 50 {
		t.Fatalf("expected focused component width 50 at 132 columns, got %d", focusedWidth)
	}
}

func TestLeftAndRightArrowsNavigatePanes(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 100
	model.windowHeight = 28
	model.focusPane = FocusValues

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m := updated.(Model)
	if m.focusPane != FocusComponents {
		t.Fatalf("left arrow did not focus Components pane, focus is %v", m.focusPane)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.focusPane != FocusValues {
		t.Fatalf("right arrow did not focus Values pane, focus is %v", m.focusPane)
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

	view := model.View()
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
			for index, item := range model.filteredValues {
				if !tc.match(item) {
					continue
				}
				model.cursor = index
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

func TestComponentAutoScrollsWhenTooLong(t *testing.T) {
	longComp := "core-base/istio-controlplane/istiod-with-a-very-long-name-that-does-not-fit-in-the-sidebar"
	model := Model{
		components:      map[string]*introspect.IntrospectResult{longComp: {ChartName: "istiod"}},
		chartNames:      []string{longComp},
		currentChart:    longComp,
		componentCursor: 0,
		focusPane:       FocusComponents,
		windowWidth:     100,
		windowHeight:    28,
		compScroll:      0,
		compScrollPause: 0,
	}

	initial := model.View()
	if !strings.Contains(initial, "> ●") {
		t.Fatalf("expected current chart cursor in component pane:\n%s", initial)
	}

	model.stepComponentScroll()
	if model.compScroll <= 0 {
		t.Fatalf("expected compScroll to advance automatically, got %d", model.compScroll)
	}

	scrolled := model.View()
	if !strings.Contains(scrolled, "‹") {
		t.Fatalf("expected component view to render scroll indicator ‹, got:\n%s", scrolled)
	}
}
func TestBundleAuthoringRemainsReachableBelowDetailsFold(t *testing.T) {
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
	if strings.Contains(model.View(), "UDS AUTHORING") {
		t.Fatal("expected authoring section below the initial Details viewport")
	}

	for range 16 {
		updated, _ = model.Update(key("j"))
		model = updated.(Model)
	}
	if !strings.Contains(model.View(), "UDS AUTHORING") {
		t.Fatalf("expected j to scroll to UDS authoring section, got:\n%s", model.View())
	}

}

func TestDetailsLabelsIdentityAndUsesChartRelativeLocations(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 110
	model.windowHeight = 32
	model.filteredValues[0].Locations = []introspect.ValueUsageLocation{
		{File: "/tmp/istiod/values.yaml", Line: 4, Column: 1},
		{File: "/tmp/istiod/charts/child/templates/workload.yaml", Line: 7, Column: 2},
		{File: "/tmp/istiod/templates/deployment.yaml", Line: 148, Column: 12},
	}
	model.focusPane = FocusInspector
	model.buildDetailItems()
	body, _ := model.inspectorContent(106, true)
	for _, want := range []string{
		"values.yaml:4:1", "charts/child/templates/workload.yaml:7:2", "templates/deployment.yaml:148:12",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("details missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "/tmp/istiod") {
		t.Fatalf("details contains an absolute source path:\n%s", body)
	}
}

func TestDetailsKeepsSelectedLocationVisibleAcrossSections(t *testing.T) {
	model := testWorkspaceModel()
	model.windowWidth = 72
	model.windowHeight = 14
	for line := 1; line <= 15; line++ {
		model.filteredValues[0].Locations = append(model.filteredValues[0].Locations, introspect.ValueUsageLocation{
			File: fmt.Sprintf("/tmp/chart/templates/subdir/%02d.yaml", line), Line: line, Column: 2,
		})
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	for range 15 {
		updated, _ = model.Update(key("j"))
		model = updated.(Model)
	}
	if model.detailCursor != 15 {
		t.Fatalf("selected action = %d, want 15", model.detailCursor)
	}
	if !strings.Contains(model.View(), "> templates/subdir/15.yaml:15:2") {
		t.Fatalf("selected source should remain visible with its directory:\n%s", model.View())
	}
	for range 15 {
		updated, _ = model.Update(key("k"))
		model = updated.(Model)
	}
	if !strings.Contains(model.View(), "> templates/deployment.yaml:148:12") {
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

	for _, shortcut := range []string{"c", " "} {
		updated, _ := model.Update(key(shortcut))
		model = updated.(Model)
		if len(model.filteredValues) != 2 || len(model.filteredValues[0].Locations) != 2 {
			t.Fatalf("%q changed consolidated values: %+v", shortcut, model.filteredValues)
		}
		if !strings.Contains(model.View(), "2 locations") {
			t.Fatalf("%q removed the consolidated source count", shortcut)
		}
	}
	if got := commandMatchCount("consolidated locations"); got != 0 {
		t.Fatalf("consolidation command remains available: %d matches", got)
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if len(model.detailItems) != 2 {
		t.Fatalf("Details should list both source locations, got %+v", model.detailItems)
	}
	for _, location := range []string{"deployment.yaml:148:12", "service.yaml:20:3"} {
		if !strings.Contains(model.View(), location) {
			t.Fatalf("Details did not render %s", location)
		}
	}
}

func TestPackageValuesWithoutChartsRemainSearchableAndShowProvenance(t *testing.T) {
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{},
		PackageValues: []introspect.PackageValues{{
			PackageName: "api",
			Values:      map[string]interface{}{"database": map[string]interface{}{"host": "db.internal"}},
			Schema:      map[string]interface{}{"type": "object"},
			Files:       []string{"../../src/my-package/zarf-values.yaml", "zarf-values.yaml"},
			FileSources: []string{"/workspace/src/my-package/zarf-values.yaml", "/workspace/zarf-values.yaml"},
			Usages: []introspect.PackageValueUsage{{
				Path: ".database.host", ComponentName: "server", ChartName: "app",
				TargetPath: "database.hostname", ExcludePaths: []string{"secret"},
				Runtime: true,
			}},
		}},
	})
	if len(model.filteredValues) != 1 || model.filteredValues[0].Path != "database.host" {
		t.Fatalf("package leaf should appear once without charts: %+v", model.filteredValues)
	}
	model.filterInput.SetValue("database.hostname")
	model.updateFilteredValues()
	if len(model.filteredValues) != 1 || model.filteredValues[0].MappingStatus != "runtime" {
		t.Fatalf("mapping target should find its configured value: %+v", model.filteredValues)
	}
	model.buildDetailItems()
	body, _ := model.inspectorContent(100, true)
	for _, want := range []string{"database.host", "runtime", "server/app", "NOT DEPLOYMENT EFFECTIVE"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in mapping details: %s", want, body)
		}
	}
	if strings.Contains(body, "EFFECTIVE VALUE") {
		t.Fatalf("runtime mapping cannot show an effective value: %s", body)
	}
	model.filterInput.SetValue("zarf-values.yaml")
	model.updateFilteredValues()
	model.buildDetailItems()
	model.focusPane = FocusInspector
	body, _ = model.inspectorContent(100, true)
	for _, want := range []string{"src/my-package/zarf-values.yaml", "zarf-values.yaml", "PACKAGE VALUES SCHEMA · HIDDEN", "db.internal"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in package values details: %s", want, body)
		}
	}
	if strings.Contains(body, "type: object") || strings.Contains(body, "../..") {
		t.Fatalf("schema should be collapsed and file paths should not traverse upward: %s", body)
	}
	if len(model.detailItems) != 3 || model.detailItems[1].Kind != DetailItemFile || model.detailItems[1].File != "/workspace/src/my-package/zarf-values.yaml" {
		t.Fatalf("values files should be selectable in declared order: %+v", model.detailItems)
	}
	updated, _ := model.Update(key("s"))
	model = updated.(Model)
	body, _ = model.inspectorContent(100, true)
	if !strings.Contains(body, "type: object") || strings.LastIndex(body, "PACKAGE VALUES SCHEMA") < strings.LastIndex(body, "INPUT FILES") {
		t.Fatalf("expanded schema should render at the bottom: %s", body)
	}
}

func TestDefaultValueStyleHasNoBackground(t *testing.T) {
	if _, ok := detailDefaultStyle.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatal("default values should not have a background color")
	}
}

func TestSingleInputFileOmitsOrderingNote(t *testing.T) {
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{},
		PackageValues: []introspect.PackageValues{{
			PackageName: "api",
			Files:       []string{"values.yaml"},
			FileSources: []string{"/cache/package/values.yaml"},
		}},
	})
	model.focusPane = FocusInspector
	model.buildDetailItems()
	body, _ := model.inspectorContent(100, true)
	if !strings.Contains(body, "values.yaml") || strings.Contains(body, "Declared order") || strings.Contains(body, "take precedence") {
		t.Fatalf("single input file should not describe ordering or precedence: %s", body)
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
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.MappingChart == "web" {
			model.cursor = i
			break
		}
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
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

func TestMappingInputDistinguishesAbsentAndExplicitValues(t *testing.T) {
	values := map[string]interface{}{"nil": nil, "off": false, "zero": 0, "empty": "", "object": map[string]interface{}{}}
	for path, want := range map[string]interface{}{"nil": nil, "off": false, "zero": 0, "empty": "", "object": map[string]interface{}{}} {
		got, present := mappingInput(values, path)
		if !present || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, present %v; want %v", path, got, present, want)
		}
	}
	for _, path := range []string{"absent", "object.child", "off.child"} {
		if got, present := mappingInput(values, path); present {
			t.Errorf("%s unexpectedly supplied: %v", path, got)
		}
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
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.PackageName == "b" {
			model.cursor = i
			break
		}
	}
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if model.mapping == nil || model.mapping.pkg.PackageName != "b" || model.mapping.inside != 1 || model.mapping.rows[0].present {
		t.Fatalf("missing-source declaration must reveal chart candidates owned by b: %+v", model.mapping)
	}
	if !strings.Contains(model.View(), "Source absent") {
		t.Fatalf("missing source label: %s", model.View())
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
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
	view := model.View()
	if !strings.Contains(view, "Zarf Package Values · api") || !strings.Contains(view, "Zarf Package Values · worker") {
		t.Fatalf("each package must have its own visible band:\n%s", view)
	}
}

func TestPackageRoutesRespectRootAndPathSegments(t *testing.T) {
	usages := []introspect.PackageValueUsage{
		{Path: ".", TargetPath: ".config", ExcludePaths: []string{".app.secret"}},
		{Path: ".app", TargetPath: ".", ExcludePaths: []string{".app.secret"}},
	}
	routes := packageRoutes("app.replicas", usages)
	if len(routes) != 2 || routes[0].Target != "config.app.replicas" || routes[1].Target != "replicas" {
		t.Fatalf("root and subtree targets: %+v", routes)
	}
	routes = packageRoutes("app.secretExtra", usages)
	if len(routes) != 2 || packageStatus(routes) != "mapped" {
		t.Fatalf("excludePaths must match whole segments: %+v", routes)
	}
	routes = packageRoutes("app.secret", usages)
	if len(routes) != 2 || packageStatus(routes) != "excluded" {
		t.Fatalf("excluded descendants: %+v", routes)
	}
	root := packageRoutes("app", []introspect.PackageValueUsage{{Path: ".app", TargetPath: "."}})
	if len(root) != 1 || root[0].Target != "." {
		t.Fatalf("chart root destination must be visible: %+v", root)
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
	if len(model.filteredValues) != 3 {
		t.Fatalf("Next defaults, package inputs, and chart should coexist: %+v", model.filteredValues)
	}
	for _, value := range model.filteredValues {
		if value.IsPackageValue {
			model.filterInput.SetValue("values/api/0.yaml")
			model.updateFilteredValues()
			model.buildDetailItems()
			body, _ := model.inspectorContent(100, true)
			for _, want := range []string{"platform 2", "oci://registry/api", "values/api/0.yaml", "base", "team", "server"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q in Next package details: %s", want, body)
				}
			}
			break
		}
	}
	model.detailBundleMode = true
	if snippet := model.generateBundleSnippetForItem(DisplayItem{Component: chart, ValueUsage: introspect.ValueUsage{Path: "image.tag"}}); snippet != "" {
		t.Fatalf("Next chart must not suggest legacy uds-bundle.yaml: %s", snippet)
	}
}

func testWorkspaceModel() Model {
	const component = "core-base/istio-controlplane/istiod"
	return NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{

			component: {
				ChartName:    "istiod",
				ChartVersion: "1.27.1",
				Source:       "/tmp/istiod",
				Values: []introspect.ValueUsage{
					{
						Path:         "global.proxy.resources.requests.memory",
						Chart:        "istiod",
						DefaultValue: "128Mi",
						Locations: []introspect.ValueUsageLocation{
							{File: "/tmp/istiod/templates/deployment.yaml", Line: 148, Column: 12, Kind: "field"},
						},
					},
					{Path: "pilot.autoscaleEnabled", Chart: "istiod", DefaultValue: true},
				},
			},
		},
		ComponentOrder: []string{component},
	})
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
	if len(model.detailItems) != 3 {
		t.Fatalf("want two owning-package mappings and one source location: %+v", model.detailItems)
	}
	first, second := model.detailItems[0].packageMapping, model.detailItems[1].packageMapping
	if first.pkg.PackageName != "b" || !first.row.present || first.row.input != false ||
		first.row.packagePath != ".app.enabled" || second.row.status != "excluded" ||
		!second.row.present || second.row.input != nil || second.row.exclusion != ".fallback.enabled" {
		t.Fatalf("mapping precedence/provenance lost: %+v %+v", first, second)
	}
	if !reflect.DeepEqual(first.pkg.Files, []string{"packaged.yaml", "operator.yaml"}) || model.detailItems[2].Location != location {
		t.Fatal("ordered inputs or editor location lost")
	}
	direct, _ := model.inspectorContent(116, true)
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
	press("j")
	if model.detailCursor != 1 {
		t.Fatal("mapping-origin Details cannot select another mapping")
	}
	press("enter")
	if model.focusPane != FocusMapping || model.mapping.usage.Path != ".fallback" {
		t.Fatal("alternative mapping link did not open its declaration")
	}
	press("esc")
	if model.focusPane != FocusInspector || !model.mappingRowDetail || model.mapping != table || model.detailCursor != 1 {
		t.Fatal("nested mapping did not restore originating Details selection")
	}
	press("j")
	if model.detailItems[model.detailCursor].Location != location {
		t.Fatal("mapping-origin Details cannot select the template location")
	}
	press("esc")
	if model.focusPane != FocusMapping || model.mapping != table {
		t.Fatal("mapping-origin Details did not return to its table")
	}
	press("esc")
	if model.focusPane != FocusInspector || model.mapping != nil || model.mappingRowDetail || model.cursor != originCursor {
		t.Fatal("table did not restore main chart Details")
	}
	press("esc")
	if model.focusPane != FocusValues || model.cursor != originCursor {
		t.Fatal("main chart Details did not preserve workspace selection")
	}
	for i, item := range model.filteredValues {
		if item.IsPackageMapping && item.PackageName == "b" && item.ValueSource == ".app" {
			model.cursor = i
			break
		}
	}
	packageCursor := model.cursor
	press("enter")
	press("enter")
	fromPackage, _ := model.inspectorContent(116, true)
	if fromPackage != direct {
		t.Fatalf("package entry lost common chart Details:\n%s", fromPackage)
	}
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
	model.filterInput.SetValue("app")
	model.updateFilteredValues()
	model.cursor = 0
	for _, value := range model.filteredValues {
		if value.IsUDSOverrideValue || value.IsUDSOverrideVar {
			t.Fatal("fixture must hide related values from workspace")
		}
	}
	press("enter")
	press("enter")
	mapped, _ := model.inspectorContent(116, true)
	if mapped != direct || len(model.detailItems) != 4 {
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
	press("esc")
	if model.focusPane != FocusMapping || model.filterInput.Value() != "app" || model.cursor != 0 {
		t.Fatal("related navigation changed the mapping origin or workspace filter")
	}
}
