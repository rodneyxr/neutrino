package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func persistentWorkspaceModel(t *testing.T) Model {
	t.Helper()
	source := filepath.Join(t.TempDir(), "templates", "deployment.yaml")
	writeModelTestSource(t, source)
	locations := make([]introspect.ValueUsageLocation, 16)
	for i := range locations {
		locations[i] = introspect.ValueUsageLocation{File: source, Line: i + 1, Column: 2, Kind: "field"}
	}
	m := NewModel(introspect.InspectionReport{
		Bundle: &introspect.BundleMetadata{Format: "next", Name: "demo", Packages: []introspect.BundlePackageMetadata{{ID: "api"}}},
		Components: map[string]*introspect.IntrospectResult{"api/server/app": {
			ChartName: "app",
			Values: []introspect.ValueUsage{
				{Path: "alpha", DefaultValue: "ALPHA_DEFAULT", DefaultPresent: true, Locations: locations},
				{Path: "beta", DefaultValue: "BETA_DEFAULT", DefaultPresent: true, Locations: locations},
				{Path: "gamma", DefaultValue: "GAMMA_DEFAULT", DefaultPresent: true},
			},
		}},
		ComponentOrder: []string{"api/server/app"},
	})
	m.selectNextCategory(5)
	return m
}

func pressWorkspaceKey(model *Model, value string) {
	msg := key(value)
	switch value {
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyMsg{Type: tea.KeyShiftTab}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	}
	updated, _ := model.Update(msg)
	*model = updated.(Model)
}

func resizeWorkspace(model *Model, width, height int) {
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	*model = updated.(Model)
}

func workspaceColumns(view string, start, width int) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, line := range lines {
		lines[i] = ansi.Cut(line, start, start+width)
	}
	return strings.Join(lines, "\n")
}

// Pane checks omit header/footer hints: "f Scope" must remain discoverable
// even when no Scope pane is currently visible.
func workspacePaneColumns(view string, start, width int) string {
	lines := strings.Split(view, "\n")
	if len(lines) > 2 {
		lines = lines[1 : len(lines)-1]
	}
	return workspaceColumns(strings.Join(lines, "\n"), start, width)
}

func workspacePaneHeading(view string, start, width int) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) < 3 {
		return ""
	}
	return strings.Trim(ansi.Cut(lines[2], start, start+width), " ║│")
}

func assertWorkspaceSize(t *testing.T, model Model, width, height int) string {
	t.Helper()
	view := model.View()
	if lipgloss.Width(view) != width || lipgloss.Height(view) != height {
		t.Fatalf("workspace at %dx%d rendered %dx%d:\n%s", width, height, lipgloss.Width(view), lipgloss.Height(view), view)
	}
	return ansi.Strip(view)
}

func TestPersistentWorkspaceWidePanesTrackSelectedSetting(t *testing.T) {
	for _, size := range []struct{ width, height, settings, inspector int }{
		{120, 32, 66, 54}, {160, 40, 88, 72},
	} {
		t.Run(fmt.Sprint(size.width), func(t *testing.T) {
			m := persistentWorkspaceModel(t)
			resizeWorkspace(&m, size.width, size.height)
			view := assertWorkspaceSize(t, m, size.width, size.height)
			for _, pane := range []struct {
				name         string
				start, width int
			}{
				{"Settings", 0, size.settings}, {"Inspector", size.settings, size.inspector},
			} {
				if !strings.Contains(workspacePaneColumns(view, pane.start, pane.width), pane.name) {
					t.Fatalf("%s missing from its persistent column:\n%s", pane.name, view)
				}
			}
			if workspacePaneHeading(view, 0, size.settings) != "Settings" || !strings.Contains(view, "f Scope") {
				t.Fatal("initial workspace must collapse Scope while exposing its shortcut")
			}
			inspector := workspaceColumns(view, size.settings, size.inspector)
			if !strings.Contains(inspector, "alpha") || !strings.Contains(inspector, "ALPHA_DEFAULT") || m.focusPane != FocusValues {
				t.Fatalf("selected setting is not inspected before Enter:\n%s", inspector)
			}
			pressWorkspaceKey(&m, "j")
			view = assertWorkspaceSize(t, m, size.width, size.height)
			inspector = workspaceColumns(view, size.settings, size.inspector)
			if m.focusPane != FocusValues || !strings.Contains(inspector, "beta") || !strings.Contains(inspector, "BETA_DEFAULT") || strings.Contains(inspector, "ALPHA_DEFAULT") {
				t.Fatalf("moving in Settings did not update the live inspector:\n%s", inspector)
			}
			pressWorkspaceKey(&m, "enter")
			if m.focusPane != FocusInspector || !strings.Contains(m.View(), "Settings") || m.workspaceGeometry(size.width).scope != 0 {
				t.Fatal("Enter should focus the existing inspector and keep Scope collapsed")
			}
		})
	}
}

func TestPersistentWorkspaceFocusCyclesVisiblePanes(t *testing.T) {
	for _, width := range []int{40, 80, 120, 160} {
		m := persistentWorkspaceModel(t)
		resizeWorkspace(&m, width, 24)
		start := m.cursor
		forward := []FocusPane{FocusInspector, FocusValues}
		backward := []FocusPane{FocusInspector, FocusValues}
		if width >= 120 {
			forward = []FocusPane{FocusInspector, FocusComponents, FocusValues}
			backward = []FocusPane{FocusComponents, FocusInspector, FocusValues}
		}
		for _, sequence := range []struct {
			key   string
			panes []FocusPane
		}{{"tab", forward}, {"shift+tab", backward}} {
			for _, want := range sequence.panes {
				pressWorkspaceKey(&m, sequence.key)
				if m.focusPane != want || m.cursor != start {
					t.Fatalf("%d columns %s: focus=%v want=%v, setting=%d want=%d", width, sequence.key, m.focusPane, want, m.cursor, start)
				}
			}
		}
	}
}

func TestPersistentWorkspaceFocusedInspectorUsesConstrainedTerminalWidth(t *testing.T) {
	for _, width := range []int{40, 80, 99, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := persistentWorkspaceModel(t)
			resizeWorkspace(&m, width, 24)
			m.filterInput.SetValue("alpha")
			m.updateFilteredValues()
			selected := m.inspectorSelection
			pressWorkspaceKey(&m, "enter")
			view := assertWorkspaceSize(t, m, width, 24)
			geometry := m.workspaceGeometry(width)
			if width < 100 {
				if geometry.settings != 0 || m.inspectorWidth() != width || workspacePaneHeading(view, 0, width) != "Inspector" {
					t.Fatalf("focused Inspector should use the whole constrained terminal:\n%s", view)
				}
			} else if geometry.settings == 0 || !strings.Contains(view, "Settings") {
				t.Fatalf("100 columns should retain the two-pane workspace:\n%s", view)
			}
			pressWorkspaceKey(&m, "esc")
			view = assertWorkspaceSize(t, m, width, 24)
			if m.focusPane != FocusValues || m.workspaceGeometry(width).settings == 0 || m.filterInput.Value() != "alpha" || m.inspectorSelection != selected || !strings.Contains(view, "Settings") {
				t.Fatal("returning from Inspector lost Settings, query, or selection")
			}
		})
	}
}

func TestPersistentWorkspaceKeepsWholeWrappedPanelActionVisible(t *testing.T) {
	m := scopedWorkspaceModel()
	resizeWorkspace(&m, 30, 12)
	pressWorkspaceKey(&m, "5")
	pressWorkspaceKey(&m, "enter")
	value, ok := m.detailValue()
	if !ok || len(m.detailItems) == 0 || m.detailItems[0].Kind != DetailItemPanel || m.detailItems[0].panel != detailAffected {
		t.Fatal("fixture must start on the underlying-values panel action")
	}
	label := ""
	for _, line := range m.inspectorDocument(value).lines {
		if line.action != nil {
			label = line.text
			break
		}
	}
	inner := max(1, m.inspectorWidth()-4)
	wrapped := strings.Split(wrapCell(label, max(1, inner-2)), "\n")
	if len(wrapped) < 2 {
		t.Fatal("fixture must wrap a panel action over multiple lines")
	}
	_, selectedRow := m.inspectorContent(inner, true)
	view := assertWorkspaceSize(t, m, 30, 12)
	visible := strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(view, "║", ""), "│", "")), "")
	if selectedRow < m.detailScrollOffset || selectedRow+len(wrapped) > m.detailScrollOffset+m.inspectorVisibleLines() || !strings.Contains(visible, strings.Join(strings.Fields(label), "")) {
		t.Fatalf("selected panel action must remain visible in full:\n%s", view)
	}
}

func TestPersistentWorkspaceSearchCaretSurvivesLongQuery(t *testing.T) {
	m := persistentWorkspaceModel(t)
	m.inputMode = InputModeFilter
	m.filterInput.SetValue("pkg:long-package-name path:some.deep.configuration.value")
	for _, width := range []int{32, 42, 58} {
		for _, position := range []int{0, 25, 40, len(m.filterInput.Value())} {
			m.filterInput.SetCursor(position)
			line := m.settingsQueryLine(width)
			if !strings.Contains(line, "▏") || lipgloss.Width(line) > width {
				t.Fatalf("search lost its edit cue at width %d position %d: %q", width, position, line)
			}
		}
	}
}

func TestPersistentWorkspaceMediumUsesScopeOverlay(t *testing.T) {
	m := persistentWorkspaceModel(t)
	resizeWorkspace(&m, 80, 24)
	view := assertWorkspaceSize(t, m, 80, 24)
	if workspacePaneHeading(view, 0, 36) != "Settings" || workspacePaneHeading(view, 36, 44) != "Inspector" {
		t.Fatalf("medium workspace should keep Settings and Inspector together:\n%s", view)
	}
	pressWorkspaceKey(&m, "enter")
	pressWorkspaceKey(&m, "f")
	if m.inputMode != InputModeScopeSelect || !strings.Contains(strings.ToLower(m.View()), "scope") {
		t.Fatal("f did not open the scope chooser")
	}
	pressWorkspaceKey(&m, "esc")
	if m.inputMode != InputModeNormal || m.focusPane != FocusInspector || m.currentChart != "All Charts" || m.cursor != 0 {
		t.Fatal("canceling scope changed focus or selected setting")
	}
}

func TestPersistentWorkspaceNarrowShowsFocusedPane(t *testing.T) {
	m := persistentWorkspaceModel(t)
	resizeWorkspace(&m, 40, 12)
	view := assertWorkspaceSize(t, m, 40, 12)
	if !strings.Contains(view, "Settings") || strings.Contains(view, "Inspector") {
		t.Fatalf("narrow Settings view contains another pane:\n%s", view)
	}
	footer := strings.Split(view, "\n")[11]
	for _, hint := range []string{"enter view", "esc back", "? help", "q quit"} {
		if !strings.Contains(footer, hint) {
			t.Fatalf("40-column entry screen lost its %q hint: %q", hint, footer)
		}
	}
	pressWorkspaceKey(&m, "enter")
	view = assertWorkspaceSize(t, m, 40, 12)
	if m.focusPane != FocusInspector || !strings.Contains(view, "Inspector") || !strings.Contains(view, "ALPHA_DEFAULT") || strings.Contains(view, "Settings") {
		t.Fatalf("narrow Inspector lost selected value:\n%s", view)
	}
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusValues || m.cursor != 0 {
		t.Fatal("narrow Inspector did not return to the selected setting")
	}
}

func TestPersistentWorkspaceRetainsScopeWhenShrinking(t *testing.T) {
	m := persistentWorkspaceModel(t)
	resizeWorkspace(&m, 160, 40)
	pressWorkspaceKey(&m, "shift+tab")
	if m.focusPane != FocusComponents {
		t.Fatal("fixture did not focus Scope")
	}
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		resizeWorkspace(&m, size[0], size[1])
		view := assertWorkspaceSize(t, m, size[0], size[1])
		if m.focusPane != FocusComponents || !strings.Contains(view, "Scope") {
			t.Fatalf("shrinking discarded active Scope:\n%s", view)
		}
	}
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusValues || m.currentChart != "All Charts" {
		t.Fatal("Escape from shrunken Scope changed the applied scope")
	}
}

func TestPersistentWorkspacePreservesSecondaryPageAcrossFocusAndResize(t *testing.T) {
	m := persistentWorkspaceModel(t)
	m.currentChart = "api/server/app"
	m.filterInput.SetValue("kind:helm")
	m.updateFilteredValues()
	m.cursor = 1
	if m.filteredValues[m.cursor].Path != "beta" || len(m.filteredValues[m.cursor].Locations) != 16 {
		t.Fatal("fixture must select the second setting with observed sources")
	}
	resizeWorkspace(&m, 160, 24)
	pressWorkspaceKey(&m, "enter")
	openInspectorTestPanel(t, &m, detailSources)
	pressWorkspaceKey(&m, "G")
	pressWorkspaceKey(&m, "enter")
	if m.detailPanel != detailSourceFile || len(m.detailPanelHistory) != 2 || m.detailSource == nil {
		t.Fatal("fixture did not open a nested source page")
	}
	wantHistory := append([]detailPanelReturn(nil), m.detailPanelHistory...)
	wantSource := *m.detailSource
	wantCursor := m.detailCursor
	for _, size := range [][2]int{{80, 24}, {40, 12}, {120, 32}, {160, 24}} {
		resizeWorkspace(&m, size[0], size[1])
		assertWorkspaceSize(t, m, size[0], size[1])
		pressWorkspaceKey(&m, "shift+tab")
		if m.focusPane != FocusValues {
			t.Fatal("Inspector could not return focus to Settings")
		}
		pressWorkspaceKey(&m, "tab")
		if m.focusPane != FocusInspector || m.detailPanel != detailSourceFile ||
			!reflect.DeepEqual(m.detailPanelHistory, wantHistory) || m.detailSource == nil || !reflect.DeepEqual(*m.detailSource, wantSource) ||
			m.detailCursor != wantCursor ||
			m.currentChart != "api/server/app" || m.filterInput.Value() != "kind:helm" || m.category != 5 || m.cursor != 1 {
			t.Fatalf("focus/resize at %v reset browsing state: focus=%v panel=%v cursor=%d want=%d source=%+v want=%+v history=%+v want=%+v scope=%q query=%q category=%d setting=%d",
				size, m.focusPane, m.detailPanel, m.detailCursor, wantCursor, m.detailSource, wantSource, m.detailPanelHistory, wantHistory,
				m.currentChart, m.filterInput.Value(), m.category, m.cursor)
		}
		_, selectedRow := m.inspectorContent(max(1, m.inspectorWidth()-4), true)
		visible := m.inspectorVisibleLines()
		if selectedRow < m.detailScrollOffset || selectedRow >= m.detailScrollOffset+visible ||
			m.detailScrollOffset < 0 || m.detailScrollOffset > m.detailScrollLimit() ||
			!strings.Contains(m.View(), "> Open file in editor") {
			t.Fatalf("focus/resize at %v hid the selected source action: row=%d offset=%d visible=%d limit=%d\n%s",
				size, selectedRow, m.detailScrollOffset, visible, m.detailScrollLimit(), m.View())
		}
	}
	pressWorkspaceKey(&m, "esc")
	if m.detailPanel != detailSources || m.detailCursor != 15 {
		t.Fatal("source page did not restore the prior source selection")
	}
	pressWorkspaceKey(&m, "shift+tab")
	pressWorkspaceKey(&m, "j")
	value, ok := m.detailValue()
	if !ok || value.Path != "gamma" || m.detailPanel != detailSummary || len(m.detailPanelHistory) != 0 || m.detailSource != nil {
		t.Fatalf("new setting retained stale inspector route: value=%+v panel=%v", value, m.detailPanel)
	}
}

func TestPersistentWorkspaceRuntimeSummaryOffersCompactSources(t *testing.T) {
	m, value, file := inspectorRuntimeFixture(t, ".Variables.CA_BUNDLE_CERTS", "runtimeContext", 37)
	m.detailRef = nil
	m.filteredValues = []DisplayItem{value}
	m.cursor, m.focusPane = 0, FocusValues
	m.buildDetailItems()
	for _, size := range []struct{ width, start, inspector int }{{120, 66, 54}, {160, 88, 72}} {
		resizeWorkspace(&m, size.width, 32)
		view := assertWorkspaceSize(t, m, size.width, 32)
		inspector := workspaceColumns(view, size.start, size.inspector)
		if strings.Contains(view, "Zarf Package Values") {
			t.Fatalf("runtime owner band invented a Package Values identity:\n%s", view)
		}
		for _, want := range []string{"CA_BUNDLE_CERTS", "Zarf runtime reference", "Requires deployment context", "Sources (1)", "Template uses"} {
			if !strings.Contains(inspector, want) {
				t.Fatalf("persistent runtime inspector lost %q:\n%s", want, inspector)
			}
		}
		if strings.Contains(inspector, filepath.Base(file)) || strings.Contains(inspector, "neutrino-cache") {
			t.Fatalf("runtime summary should group source paths behind the Sources action: %s", inspector)
		}
		for _, unwanted := range []string{".Values.Variables", "Helm .Values", "Input history", ".vars.scale"} {
			if strings.Contains(inspector, unwanted) {
				t.Fatalf("primary runtime inspector disclosed %q:\n%s", unwanted, inspector)
			}
		}
	}
}

func TestPersistentWorkspaceRelatedValueSurvivesFocusChanges(t *testing.T) {
	const component = "api/server/app"
	m := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{component: {
			Values:               []introspect.ValueUsage{{Path: "alpha", DefaultValue: true}, {Path: "beta", DefaultValue: false}},
			UDSOverrideVariables: []introspect.UDSOverrideVariable{{Name: "ENABLED", Path: "alpha", Default: true, Description: "Enable service"}},
		}},
		ComponentOrder: []string{component},
	})
	resizeWorkspace(&m, 120, 32)
	for index, item := range m.filteredValues {
		if item.Path == "alpha" && !item.IsUDSOverrideVar {
			m.cursor = index
			break
		}
	}
	pressWorkspaceKey(&m, "enter")
	found := false
	for index, detail := range m.detailItems {
		if detail.Kind == DetailItemCrossRef && detail.relatedValue != nil && detail.relatedValue.IsUDSOverrideVar {
			m.detailCursor, found = index, true
			break
		}
	}
	if !found {
		t.Fatal("fixture has no related UDS variable action")
	}
	pressWorkspaceKey(&m, "enter")
	for _, pane := range []FocusPane{FocusComponents, FocusValues, FocusInspector} {
		pressWorkspaceKey(&m, "tab")
		value, ok := m.detailValue()
		if m.focusPane != pane || !ok || value.Path != "ENABLED" || !value.IsUDSOverrideVar || m.detailRef == nil {
			t.Fatalf("focus %v lost the related inspector value: %+v", pane, value)
		}
	}
	pressWorkspaceKey(&m, "shift+tab")
	updated, copyCommand := m.Update(key("y"))
	m = updated.(Model)
	if copyCommand == nil || m.statusMessage != "Copied alpha" {
		t.Fatalf("Settings copy used pinned inspector value instead of highlighted alpha: %q", m.statusMessage)
	}
	pressWorkspaceKey(&m, "enter")
	value, ok := m.detailValue()
	if !ok || value.Path != "alpha" || m.detailRef != nil || len(m.traceHistory) != 0 || m.detailPanel != detailSummary {
		t.Fatalf("Enter did not explicitly inspect the highlighted setting: %+v", value)
	}
	pressWorkspaceKey(&m, "shift+tab")
	pressWorkspaceKey(&m, "j")
	value, ok = m.detailValue()
	if !ok || value.Path != "beta" || m.detailRef != nil || m.detailPanel != detailSummary {
		t.Fatalf("new setting did not replace the related inspector route: %+v", value)
	}
}

func TestPersistentWorkspaceRefocusKeepsResizedSourceActionVisible(t *testing.T) {
	m := persistentWorkspaceModel(t)
	resizeWorkspace(&m, 160, 40)
	pressWorkspaceKey(&m, "enter")
	openInspectorTestPanel(t, &m, detailSources)
	pressWorkspaceKey(&m, "G")
	if m.detailCursor != 15 || len(m.detailItems) != 16 {
		t.Fatal("fixture must select the last of sixteen observed sources")
	}
	wantHistory := append([]detailPanelReturn(nil), m.detailPanelHistory...)
	wantSource := m.detailItems[m.detailCursor]
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		pressWorkspaceKey(&m, "shift+tab")
		if m.focusPane != FocusValues {
			t.Fatal("fixture did not return focus to Settings before resizing")
		}
		resizeWorkspace(&m, size[0], size[1])
		if m.focusPane != FocusValues {
			t.Fatalf("resize at %v changed Settings focus", size)
		}
		pressWorkspaceKey(&m, "tab")
		view := assertWorkspaceSize(t, m, size[0], size[1])
		if m.focusPane != FocusInspector || m.detailPanel != detailSources || m.detailCursor != 15 || len(m.detailItems) != 16 ||
			!reflect.DeepEqual(m.detailPanelHistory, wantHistory) || !reflect.DeepEqual(m.detailItems[m.detailCursor], wantSource) {
			t.Fatalf("refocus at %v changed source navigation: focus=%v panel=%v cursor=%d history=%+v", size, m.focusPane, m.detailPanel, m.detailCursor, m.detailPanelHistory)
		}
		_, selectedRow := m.inspectorContent(max(1, m.inspectorWidth()-4), true)
		visible := m.inspectorVisibleLines()
		if selectedRow < m.detailScrollOffset || selectedRow >= m.detailScrollOffset+visible || !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(view, "║", ""), "│", "")), ""), "deployment.yaml:16") {
			t.Fatalf("refocus after resizing at %v hid the selected source: row=%d offset=%d visible=%d\n%s", size, selectedRow, m.detailScrollOffset, visible, view)
		}
	}
}

func TestPersistentWorkspaceScopeCollapsesWithoutChangingInspectorRoute(t *testing.T) {
	for _, width := range []int{120, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := persistentWorkspaceModel(t)
			resizeWorkspace(&m, width, 32)
			m.filterInput.SetValue("kind:helm")
			m.updateFilteredValues()
			pressWorkspaceKey(&m, "j")
			selected, selectionKey := m.cursor, m.inspectorSelection
			pressWorkspaceKey(&m, "shift+tab")
			geometry := m.workspaceGeometry(width)
			view := assertWorkspaceSize(t, m, width, 32)
			if m.focusPane != FocusComponents || geometry.scope == 0 || workspacePaneHeading(view, 0, geometry.scope) != "Scope" ||
				m.filterInput.Width != geometry.settings-6 {
				t.Fatalf("ShiftTab did not open Scope with matching input geometry:\n%s", view)
			}
			pressWorkspaceKey(&m, "tab")
			if m.focusPane != FocusValues || m.workspaceGeometry(width).scope != 0 || m.cursor != selected || m.inspectorSelection != selectionKey ||
				m.filterInput.Width != width*55/100-6 {
				t.Fatal("leaving Scope changed selection or failed to restore wider Settings")
			}
			pressWorkspaceKey(&m, "enter")
			openInspectorTestPanel(t, &m, detailSources)
			pressWorkspaceKey(&m, "G")
			pressWorkspaceKey(&m, "enter")
			if m.detailPanel != detailSourceFile || m.detailSource == nil {
				t.Fatal("fixture did not open a nested inspector source")
			}
			wantHistory := append([]detailPanelReturn(nil), m.detailPanelHistory...)
			wantSource, wantCursor, wantOffset := *m.detailSource, m.detailCursor, m.detailScrollOffset
			for range 2 {
				pressWorkspaceKey(&m, "f")
			}
			if m.scopeReturnFocus != FocusInspector || m.detailPanel != detailSourceFile {
				t.Fatal("repeated Scope entry replaced its caller or Inspector route")
			}
			pressWorkspaceKey(&m, "esc")
			view = assertWorkspaceSize(t, m, width, 32)
			if m.focusPane != FocusInspector || m.workspaceGeometry(width).scope != 0 ||
				m.detailPanel != detailSourceFile || m.detailSource == nil || !reflect.DeepEqual(*m.detailSource, wantSource) ||
				!reflect.DeepEqual(m.detailPanelHistory, wantHistory) || m.detailCursor != wantCursor || m.detailScrollOffset != wantOffset ||
				m.cursor != selected || m.inspectorSelection != selectionKey || m.filterInput.Value() != "kind:helm" || m.category != 5 {
				t.Fatalf("closing Scope changed Inspector state: focus=%v panel=%v action=%d/%d offset=%d/%d selected=%d/%d sourceEqual=%t historyEqual=%t selectionEqual=%t:\n%s",
					m.focusPane, m.detailPanel, m.detailCursor, wantCursor, m.detailScrollOffset, wantOffset, m.cursor, selected,
					m.detailSource != nil && reflect.DeepEqual(*m.detailSource, wantSource), reflect.DeepEqual(m.detailPanelHistory, wantHistory), m.inspectorSelection == selectionKey, view)
			}
			settingsWidth := width * 55 / 100
			if workspacePaneHeading(view, 0, settingsWidth) != "Settings" || workspacePaneHeading(view, settingsWidth, width-settingsWidth) != "Inspector" {
				t.Fatalf("closing Scope did not restore the two pane headings:\n%s", view)
			}
		})
	}
}

func TestPersistentWorkspaceScopeEntryFromOverviewAndCommandPalette(t *testing.T) {
	for _, entry := range []string{"f", "command"} {
		t.Run(entry, func(t *testing.T) {
			m := persistentWorkspaceModel(t)
			resizeWorkspace(&m, 160, 32)
			pressWorkspaceKey(&m, "j")
			selected, selectionKey := m.cursor, m.inspectorSelection
			pressWorkspaceKey(&m, "0")
			if !m.showOverview {
				t.Fatal("fixture did not enter Overview")
			}
			if entry == "command" {
				pressWorkspaceKey(&m, ":")
				m.commandInput.SetValue("Select package or chart")
				pressWorkspaceKey(&m, "enter")
			} else {
				pressWorkspaceKey(&m, "f")
			}
			view := assertWorkspaceSize(t, m, 160, 32)
			geometry := m.workspaceGeometry(160)
			if m.inputMode != InputModeNormal || m.showOverview || m.focusPane != FocusComponents ||
				workspacePaneHeading(view, 0, geometry.scope) != "Scope" || m.filterInput.Width != geometry.settings-6 {
				t.Fatalf("%s left Scope hidden or used stale geometry:\n%s", entry, view)
			}
			pressWorkspaceKey(&m, "esc")
			if m.focusPane != FocusValues || m.showOverview || m.workspaceGeometry(160).scope != 0 ||
				m.filterInput.Width != 88-6 || m.cursor != selected || m.inspectorSelection != selectionKey {
				t.Fatal("closing Scope did not preserve the selected setting and collapsed geometry")
			}
		})
	}
}

func TestPersistentWorkspaceScopeRoundTripKeepsMappingAndTrace(t *testing.T) {
	m := scopedWorkspaceModel()
	resizeWorkspace(&m, 160, 32)
	m.filterInput.SetValue("pkg:api")
	m.updateFilteredValues()
	m.selectNextCategory(4)
	pressWorkspaceKey(&m, "enter")
	openInspectorTestMapping(t, &m)
	table := m.mapping
	pressWorkspaceKey(&m, "j")
	wantMappingCursor, wantMappingOffset := table.cursor, table.offset
	pressWorkspaceKey(&m, "tab")
	related := DisplayItem{IsPackageValue: true, ValuePresent: true, PackageName: "api", Component: "Zarf Package Values",
		ValueUsage: introspect.ValueUsage{Path: "settings.replicas", DefaultValue: 2}}
	if !m.followTrace(DetailItem{Kind: DetailItemTrace, relatedValue: &related}) {
		t.Fatal("fixture did not follow its package-input trace")
	}
	wantTrace := append([]traceReturn(nil), m.traceHistory...)
	wantRef, wantSelection := m.detailRef, m.inspectorSelection
	pressWorkspaceKey(&m, "f")
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusInspector || m.mapping != table || m.mapping.cursor != wantMappingCursor || m.mapping.offset != wantMappingOffset ||
		m.detailRef != wantRef || !reflect.DeepEqual(m.traceHistory, wantTrace) || m.inspectorSelection != wantSelection ||
		m.filterInput.Value() != "pkg:api" || m.category != 4 || m.workspaceGeometry(160).scope != 0 {
		t.Fatal("Scope open/close reset the active mapping or pinned trace")
	}
	pressWorkspaceKey(&m, "shift+tab")
	pressWorkspaceKey(&m, "shift+tab")
	pressWorkspaceKey(&m, "tab")
	if m.focusPane != FocusMapping || m.mapping != table || m.mapping.cursor != wantMappingCursor || m.detailRef != wantRef || !reflect.DeepEqual(m.traceHistory, wantTrace) {
		t.Fatal("tabbing through Scope did not preserve the active mapped-row route")
	}
}
