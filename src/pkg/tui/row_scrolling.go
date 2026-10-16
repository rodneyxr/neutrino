package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// settingRowCells is shared by rendering and animation so offsets use the
// exact displayed, masked text and the current pane's column widths.
type settingRowCells struct {
	path, value                      string
	pathWidth, valueWidth, kindWidth int
	metadata                         bool
}

func (m Model) settingRowCells(value DisplayItem, innerWidth int) settingRowCells {
	pathWidth, valueWidth, kindWidth, _, metadata := m.columnWidths(innerWidth)
	safe := value
	safe.DefaultValue = m.displayValue(value, value.DefaultValue)
	safe.OverrideValue = m.displayValue(value, value.OverrideValue)
	cellValue := displayValue(safe)
	if value.IsPackageValue && value.Component == "UDS Next bundle" {
		cellValue = m.bundleFilesState(value)
	}
	return settingRowCells{
		path: terminalText(m.valueListPath(value)), value: terminalText(cellValue),
		pathWidth: pathWidth, valueWidth: valueWidth, kindWidth: kindWidth, metadata: metadata,
	}
}

// Offsets count terminal cells, not bytes or runes. A leading marker consumes
// one cell once scrolling starts, so the final frame can show the entire tail.
func rowScrollLimit(text string, width int) int {
	if width <= 1 {
		return 0
	}
	length := ansi.StringWidth(terminalText(text))
	if length <= width {
		return 0
	}
	return length - width + 1
}

func scrollRowCell(text string, offset, width int) string {
	text = terminalText(text)
	if width <= 0 {
		return ""
	}
	limit := rowScrollLimit(text, width)
	if offset <= 0 || limit == 0 {
		return ansi.Truncate(text, width, "…")
	}
	offset = min(offset, limit)
	tail := ansi.Cut(text, offset, ansi.StringWidth(text))
	// Cut retains a wide grapheme when the left edge lands inside it. At the
	// final frame, advance past that grapheme rather than clipping the tail.
	if offset == limit {
		for ansi.StringWidth(tail) > width-1 {
			offset++
			tail = ansi.Cut(text, offset, ansi.StringWidth(text))
		}
	}
	return "‹" + ansi.Truncate(tail, width-1, "…")
}

// padRowCell pads by terminal cells rather than fmt's rune count. This keeps
// neighboring columns aligned when a path/value contains wide graphemes.
func padRowCell(text string, width int) string {
	text = terminalText(text)
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func advanceRowScroll(limit int, offset, pause *int) {
	if limit <= 0 {
		*offset, *pause = 0, 0
		return
	}
	*offset = clamp(*offset, 0, limit)
	if *pause > 0 {
		*pause--
		return
	}
	if *offset < limit {
		*offset++
		if *offset == limit {
			*pause = 6
		}
		return
	}
	*offset, *pause = 0, 3
}

func (m *Model) resetSelectedRowScroll() {
	m.pathScroll, m.valueScroll = 0, 0
	m.pathScrollPause, m.valueScrollPause = 2, 2
}

func (m Model) rowAnimationActive() bool {
	return m.inputMode == InputModeNormal && !m.showHelp && !m.showOverview
}

func (m *Model) advanceSelectedRowScroll() {
	if !m.rowAnimationActive() || m.focusPane != FocusValues && m.focusPane != FocusMapping {
		return
	}
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	inner := max(1, m.workspaceGeometry(width).settings-4)
	pathLimit, valueLimit := 0, 0
	if m.focusPane == FocusMapping {
		if m.mapping != nil && m.mapping.cursor >= 0 && m.mapping.cursor < len(m.mapping.rows) {
			cells := m.mappingRowCells(m.mapping.rows[m.mapping.cursor], inner)
			pathLimit = rowScrollLimit(cells.path, cells.pathWidth)
			valueLimit = max(rowScrollLimit(cells.value, cells.valueWidth), rowScrollLimit(cells.input, cells.inputWidth))
		}
	} else if value, ok := m.selectedValue(); ok {
		cells := m.settingRowCells(value, inner)
		pathLimit = rowScrollLimit(cells.path, cells.pathWidth)
		valueLimit = rowScrollLimit(cells.value, cells.valueWidth)
	}
	advanceRowScroll(pathLimit, &m.pathScroll, &m.pathScrollPause)
	advanceRowScroll(valueLimit, &m.valueScroll, &m.valueScrollPause)
}

// scopeLabelCell uses the same tree prefix for the visible row and its scroll
// budget. scopeTreeKey is independent of the legacy component picker cursor.
func (m Model) scopeLabelCell(scope workspaceScope, width int) (prefix, label string, labelWidth int) {
	branch := "  "
	if m.scopeHasChildren(scope) {
		branch = "▸ "
		if m.scopeIsExpanded(scope) {
			branch = "▾ "
		}
	}
	marker := "  "
	if scope.key == m.currentChart {
		marker = "● "
	}
	if m.focusPane == FocusComponents && scope.key == m.scopeTreeKey {
		marker = "> "
	}
	prefix = marker + strings.Repeat("  ", scope.depth) + branch
	return prefix, terminalText(scope.label), max(1, width-4-ansi.StringWidth(prefix))
}

func (m *Model) advanceScopeLabelScroll() {
	if !m.rowAnimationActive() || m.focusPane != FocusComponents {
		return
	}
	visible := m.visibleScopeTree()
	if len(visible) == 0 {
		m.compScroll, m.compScrollPause = 0, 0
		return
	}
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	if paneWidth := m.workspaceGeometry(width).scope; paneWidth > 0 {
		width = paneWidth
	}
	_, label, labelWidth := m.scopeLabelCell(visible[m.scopeTreeIndex(visible)], width)
	advanceRowScroll(rowScrollLimit(label, labelWidth), &m.compScroll, &m.compScrollPause)
}
