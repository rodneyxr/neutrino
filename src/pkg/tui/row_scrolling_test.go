package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func rowScrollingModel(path string, value interface{}) Model {
	m := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{
		"server/app": {ChartName: "app", Values: []introspect.ValueUsage{{Path: path, DefaultValue: value, DefaultPresent: true}}},
	}})
	m.windowWidth, m.windowHeight, m.focusPane = 80, 24, FocusValues
	m.resetSelectedRowScroll()
	return m
}

func scrollTick(m *Model) {
	updated, _ := m.Update(pathScrollTickMsg(time.Now()))
	*m = updated.(Model)
}

func TestRowScrollTickAdvancesLongValueWithoutLongPath(t *testing.T) {
	for _, width := range []int{40, 80, 120, 160} {
		m := rowScrollingModel("image", "registry.example.test/team/very-long-image:release-tail")
		m.windowWidth = width
		before := m.View()
		for range 3 {
			scrollTick(&m)
		}
		if m.pathScroll != 0 || m.valueScroll != 1 {
			t.Fatalf("width %d: tick did not scroll value independently: path=%d value=%d", width, m.pathScroll, m.valueScroll)
		}
		if after := m.View(); before == after || !strings.Contains(ansi.Strip(after), "‹") {
			t.Fatalf("width %d: live tick did not change selected row rendering", width)
		}
	}
}

func TestRowScrollTickAdvancesPathAndPausesAtItsTail(t *testing.T) {
	m := rowScrollingModel("global.proxy.resources.requests.extremelyLongMemoryTail", "128Mi")
	value, _ := m.selectedValue()
	cells := m.settingRowCells(value, m.workspaceGeometry(m.windowWidth).settings-4)
	limit := rowScrollLimit(cells.path, cells.pathWidth)
	for range 2 {
		scrollTick(&m)
		if m.pathScroll != 0 {
			t.Fatal("initial reading pause was skipped")
		}
	}
	for range limit {
		scrollTick(&m)
	}
	if m.pathScroll != limit || m.valueScroll != 0 || m.pathScrollPause != 6 {
		t.Fatalf("tail did not pause independently: path=%d/%d value=%d pause=%d", m.pathScroll, limit, m.valueScroll, m.pathScrollPause)
	}
	if tail := scrollRowCell(cells.path, m.pathScroll, cells.pathWidth); !strings.HasSuffix(tail, "Tail") || strings.HasSuffix(tail, "…") {
		t.Fatalf("final path frame lost its tail: %q", tail)
	}
	for range 6 {
		scrollTick(&m)
		if m.pathScroll != limit {
			t.Fatal("tail reading pause was skipped")
		}
	}
	scrollTick(&m)
	if m.pathScroll != 0 || m.pathScrollPause != 3 {
		t.Fatal("scroll cycle did not restart at the beginning")
	}
}

func TestRowScrollPausesOutsideActiveRows(t *testing.T) {
	for _, paused := range []string{"filter", "command", "scope picker", "help", "overview", "inspector"} {
		t.Run(paused, func(t *testing.T) {
			m := rowScrollingModel(strings.Repeat("long.path.", 8), strings.Repeat("long value ", 8))
			m.pathScroll, m.valueScroll, m.pathScrollPause, m.valueScrollPause = 3, 4, 0, 0
			switch paused {
			case "filter":
				m.inputMode = InputModeFilter
			case "command":
				m.inputMode = InputModeCommand
			case "scope picker":
				m.inputMode = InputModeScopeSelect
			case "help":
				m.showHelp = true
			case "overview":
				m.showOverview = true
			case "inspector":
				m.focusPane = FocusInspector
			}
			scrollTick(&m)
			if m.pathScroll != 3 || m.valueScroll != 4 || m.pathScrollPause != 0 || m.valueScrollPause != 0 {
				t.Fatal("an inactive row consumed animation state")
			}
		})
	}
}

func TestRowCellsMaskBeforeMeasuringAndScrolling(t *testing.T) {
	secret := "SECRET_VALUE_MUST_NEVER_REACH_ANIMATION"
	m := rowScrollingModel("credentials", secret)
	m.windowWidth = 160
	value, _ := m.selectedValue()
	cells := m.settingRowCells(value, 90)
	if !strings.Contains(cells.value, redactedValue) || strings.Contains(cells.value, secret) {
		t.Fatalf("scroll projection did not mask value: %q", cells.value)
	}
	for range 80 {
		scrollTick(&m)
		if strings.Contains(ansi.Strip(m.View()), secret) {
			t.Fatal("scrolling exposed a masked value")
		}
	}
	if m.valueScroll != 0 {
		t.Fatal("scroll limit was derived from secret length instead of displayed mask")
	}
	malicious := rowScrollingModel("image\x1b[31m\n", "public\x1b[2J\x07")
	item, _ := malicious.selectedValue()
	projected := malicious.settingRowCells(item, 90)
	if strings.ContainsAny(projected.path+projected.value, "\x1b\x07\n") {
		t.Fatal("row projection retained terminal controls")
	}
}

func TestRowScrollUsesTerminalCellsAndPreservesUnicodeTail(t *testing.T) {
	for _, text := range []string{"界界界界界", "前缀e\u0301🙂.tail", "👩‍💻👩‍💻👩‍💻尾巴"} {
		for _, width := range []int{0, 1, 4, 5, 8} {
			limit := rowScrollLimit(text, width)
			for offset := 0; offset <= limit; offset++ {
				cell := scrollRowCell(text, offset, width)
				if !utf8.ValidString(cell) || ansi.StringWidth(cell) > width || strings.ContainsRune(cell, '\ufffd') {
					t.Fatalf("invalid cell for %q width=%d offset=%d: %q", text, width, offset, cell)
				}
				if padded := padRowCell(cell, width); ansi.StringWidth(padded) != width {
					t.Fatalf("terminal-cell padding drifted: %q", padded)
				}
			}
			if limit > 0 {
				tail := scrollRowCell(text, limit, width)
				if strings.HasSuffix(tail, "…") {
					t.Fatalf("final wide-grapheme frame still hides the tail: %q", tail)
				}
			}
		}
	}
	if got := scrollRowCell("界e\u0301.tail", 0, 4); strings.Contains(got, "e") && !strings.Contains(got, "e\u0301") {
		t.Fatalf("combining grapheme split: %q", got)
	}
}

func TestScopeLabelScrollUsesTreeCursorAndIndent(t *testing.T) {
	m := rowScrollingModel("image", "v1")
	m.windowWidth, m.focusPane = 160, FocusComponents
	m.scopes = []workspaceScope{
		{key: "All Charts", label: "All", kind: "inspection"},
		{key: "package:very-long-package-id-with-tail", label: "very-long-package-id-with-tail", kind: "package", parentKey: "All Charts", depth: 1},
	}
	m.scopeTreeKey = m.scopes[1].key
	m.componentCursor = -1 // The obsolete flat picker must not drive this tree.
	m.compScroll, m.compScrollPause = 0, 0
	prefix, label, width := m.scopeLabelCell(m.scopes[1], m.workspaceGeometry(m.windowWidth).scope)
	if prefix != ">     " || width != m.workspaceGeometry(m.windowWidth).scope-4-ansi.StringWidth(prefix) {
		t.Fatalf("tree prefix and animation budget disagree: %q width=%d", prefix, width)
	}
	m.advanceScopeLabelScroll()
	if m.compScroll != 1 || rowScrollLimit(label, width) <= 0 {
		t.Fatal("selected tree label did not advance")
	}
}

func TestMappingRowScrollUsesVisibleMaskedColumns(t *testing.T) {
	m := rowScrollingModel("image", "v1")
	row := mappingRow{path: "image", defaultValue: strings.Repeat("public-image-", 8), status: "available", value: introspect.ValueUsage{Path: "image", DefaultPresent: true}}
	m.mapping = &mappingScreen{chartKey: "server/app", rows: []mappingRow{row}}
	m.focusPane = FocusMapping
	m.resetSelectedRowScroll()
	for range 3 {
		scrollTick(&m)
	}
	if m.pathScroll != 0 || m.valueScroll != 1 || !strings.Contains(ansi.Strip(m.mappingPane(36, 20)), "‹") {
		t.Fatal("mapped default did not scroll independently in its visible column")
	}
	for _, inner := range []int{20, 32, 36, 80, 120} {
		path, status, value, input := mappingColumnWidths(inner)
		separators := 3
		if status > 0 {
			separators++
		}
		if input > 0 {
			separators++
		}
		if path+status+value+input+separators != inner {
			t.Fatalf("mapping cell budget differs from rendered width %d: %d/%d/%d/%d +%d", inner, path, status, value, input, separators)
		}
	}
}

func TestMappingRowScrollIncludesInputOnlyWhenItsColumnIsVisible(t *testing.T) {
	m := rowScrollingModel("image", "v1")
	row := mappingRow{path: "image", defaultValue: "v1", packagePath: "image", input: strings.Repeat("long-input-", 12), present: true, status: "available",
		value: introspect.ValueUsage{Path: "image", DefaultPresent: true}}
	m.mapping = &mappingScreen{chartKey: "server/app", rows: []mappingRow{row}}
	m.focusPane = FocusMapping
	for range 3 {
		scrollTick(&m)
	}
	if m.valueScroll != 0 {
		t.Fatal("hidden mapping input drove visible short default animation")
	}
	m.windowWidth = 320
	m.resetSelectedRowScroll()
	for range 3 {
		scrollTick(&m)
	}
	if m.valueScroll != 1 {
		t.Fatal("visible mapping input did not advance independently of short default")
	}
	cells := m.mappingRowCells(row, m.workspaceGeometry(m.windowWidth).settings-4)
	if got := scrollRowCell(cells.value, m.valueScroll, cells.valueWidth); got != "v1" {
		t.Fatalf("shared value clock scrolled a fitting chart default: %q", got)
	}
	row.packagePath = "apiToken"
	masked := m.mappingRowCells(row, m.workspaceGeometry(m.windowWidth).settings-4)
	if !strings.Contains(masked.input, redactedValue) || strings.Contains(masked.input, "long-input-") {
		t.Fatal("mapping input animation projection did not mask sensitive input")
	}
}
