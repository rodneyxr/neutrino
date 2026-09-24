package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
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
			want:        []string{"COMPONENTS", "[FOCUSED] VALUES", "2 panes"},
			doesNotWant: []string{"DETAILS"},
		},
		{
			name:        "medium",
			width:       100,
			height:      28,
			want:        []string{"COMPONENTS", "[FOCUSED] VALUES", "2 panes"},
			doesNotWant: []string{"DETAILS"},
		},
		{
			name:        "compact",
			width:       72,
			height:      24,
			want:        []string{"[FOCUSED] VALUES", "single pane"},
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
	if !strings.Contains(view, "[FOCUSED] DETAILS") {
		t.Fatalf("enter did not open full-width details:\n%s", view)
	}
	if strings.Contains(view, "COMPONENTS") {
		t.Fatal("details should replace the two-pane workspace")
	}

	updated, _ = details.Update(key("esc"))
	workspace := updated.(Model).View()
	if !strings.Contains(workspace, "[FOCUSED] VALUES") || !strings.Contains(workspace, "COMPONENTS") {
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
	view := m.View()
	if !strings.Contains(view, "[FOCUSED] COMPONENTS") {
		t.Fatalf("expected focused components view:\n%s", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.focusPane != FocusValues {
		t.Fatalf("right arrow did not focus Values pane, focus is %v", m.focusPane)
	}
	view = m.View()
	if !strings.Contains(view, "[FOCUSED] VALUES") {
		t.Fatalf("expected focused values view:\n%s", view)
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
		"DEFAULT VALUE",
		"requests:",
		"  cpu: 100m",
		"  memory: 128Mi",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected Details view to contain %q, got:\n%s", want, view)
		}
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
