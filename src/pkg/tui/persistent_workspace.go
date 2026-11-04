package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type workspaceGeometry struct{ scope, settings, inspector int }

func geometryFor(width int) workspaceGeometry {
	if width >= wideLayoutWidth {
		scope := clamp(24+(width-120)/10, 24, 32)
		settings := 46 + (width-120)*2/5
		return workspaceGeometry{scope: scope, settings: settings, inspector: width - scope - settings}
	}
	if width >= mediumLayoutWidth {
		settings := width * 45 / 100
		return workspaceGeometry{settings: settings, inspector: width - settings}
	}
	return workspaceGeometry{settings: width, inspector: width}
}

// Scope borrows space only while it has focus. Its cursor and expansion state
// live independently of this layout, so closing it never changes the selection.
func (m Model) workspaceGeometry(width int) workspaceGeometry {
	// A focused inspector needs enough room to retain destination identity and
	// comparison labels. Settings remains one Tab/Esc away at constrained widths.
	if width < 100 && m.focusPane == FocusInspector {
		return workspaceGeometry{inspector: width}
	}
	if width >= wideLayoutWidth && m.focusPane != FocusComponents {
		settings := width * 55 / 100
		return workspaceGeometry{settings: settings, inspector: width - settings}
	}
	return geometryFor(width)
}

func (m *Model) refreshWorkspaceGeometry() {
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	m.filterInput.Width = max(1, m.workspaceGeometry(width).settings-6)
	m.ensureCursorVisible()
	m.ensureMappingCursorVisible()
	if m.focusPane == FocusInspector {
		m.ensureDetailCursorVisible()
	}
}

func (m Model) inspectorWidth() int {
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	return m.workspaceGeometry(width).inspector
}

func (m Model) centralFocus() FocusPane {
	if m.mapping != nil {
		return FocusMapping
	}
	return FocusValues
}

func (m Model) liveSelectionKey() string {
	if m.mapping != nil {
		if m.mapping.cursor >= 0 && m.mapping.cursor < len(m.mapping.rows) {
			return fmt.Sprintf("mapping:%p:%s", m.mapping, m.mapping.rows[m.mapping.cursor].path)
		}
		return fmt.Sprintf("mapping:%p:empty", m.mapping)
	}
	value, ok := m.selectedValue()
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%s:%s:%s:%s:%s:%p", nextCategory(value), value.PackageName, value.Component, value.SummaryComponent, value.Chart, value.Path, value.mappingRef)
}

// Focus and resizing never change this key. Only a different catalog/mapping
// selection replaces the inspector's route with the newly selected summary.
func (m *Model) syncInspectorSelection() {
	key := m.liveSelectionKey()
	if key != m.inspectorSelection {
		m.resetSelectedRowScroll()
		m.inspectorSelection = key
		m.resetInspectorRoute()
		m.mappingRowDetail = m.mapping != nil && m.focusPane == FocusInspector
	}
	m.buildDetailItems()
}

func (m *Model) resetInspectorRoute() {
	m.detailRef = nil
	m.traceHistory = nil
	m.resetDetailPanels()
	m.detailCursor, m.detailScrollOffset = 0, 0
}

func (m Model) focusedActionValue() (DisplayItem, bool) {
	if m.focusPane == FocusInspector {
		return m.detailValue()
	}
	if m.focusPane == FocusMapping {
		m.detailRef = nil
		return m.detailValue()
	}
	return m.selectedValue()
}

func (m Model) persistentWorkspace(width, height int) string {
	geometry := m.workspaceGeometry(width)
	if geometry.settings == 0 && m.focusPane == FocusInspector {
		return m.inspectorPane(width, height)
	}
	if width < mediumLayoutWidth {
		switch m.focusPane {
		case FocusComponents:
			return m.scopeTreePane(width, height)
		case FocusInspector:
			return m.inspectorPane(width, height)
		default:
			if m.mapping != nil {
				return m.mappingPane(width, height)
			}
			return m.valuesPane(width, height)
		}
	}
	settings := m.valuesPane(geometry.settings, height)
	if m.mapping != nil {
		settings = m.mappingPane(geometry.settings, height)
	}
	if geometry.scope == 0 {
		if m.focusPane == FocusComponents {
			return m.scopeTreePane(width, height)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, settings, m.inspectorPane(geometry.inspector, height))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.scopeTreePane(geometry.scope, height), settings, m.inspectorPane(geometry.inspector, height))
}

func (m Model) settingsFilterLine(width int) string {
	return m.settingsTabs(width)
}

func (m Model) settingsQueryLine(width int) string {
	query := strings.TrimSpace(m.filterInput.Value())
	if m.inputMode == InputModeFilter {
		runes := []rune(terminalText(m.filterInput.Value()))
		position := clamp(m.filterInput.Position(), 0, len(runes))
		before := string(runes[:position])
		// Leave room for both the caret and a truncated suffix while editing
		// in the middle of a query wider than this pane.
		available := max(1, width-4)
		if lipgloss.Width(before) > available {
			before = tailCell(before, available)
		}
		return truncateCell("/ "+before+"▏"+string(runes[position:]), width)
	}
	if query == "" {
		return truncateCell("/ Search "+m.selectedSettingTypeLabel(), width)
	}
	return truncateCell("/ "+query, width)
}

// A bundle's file declaration is distinct from a supplied package value.
// Report inspection of those files without implying they were applied.
func (m Model) bundleFilesState(value DisplayItem) string {
	state := "declared"
	if pkg := m.tracePackage(value); pkg != nil {
		for _, source := range pkg.Sources {
			if source.Kind != "bundle-values" {
				continue
			}
			switch source.Status {
			case "resolved":
				state = "inspected"
			case "missing":
				return "missing"
			case "parse-error":
				return "parse error"
			case "unavailable":
				return "unavailable"
			default:
				return "not evaluated"
			}
		}
	}
	return state
}
