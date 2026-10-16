package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) scopeHasChildren(scope workspaceScope) bool {
	for _, candidate := range m.scopes {
		if candidate.parentKey == scope.key && candidate.kind != "inspection" {
			return true
		}
	}
	return false
}

func (m Model) scopeIsExpanded(scope workspaceScope) bool {
	if expanded, found := m.scopeExpanded[scope.key]; found {
		return expanded
	}
	return scope.kind != "package"
}

func (m *Model) setScopeExpanded(key string, expanded bool) {
	if m.scopeExpanded == nil {
		m.scopeExpanded = make(map[string]bool)
	}
	m.scopeExpanded[key] = expanded
}

func (m Model) visibleScopeTree() []workspaceScope {
	visible := make([]workspaceScope, 0, len(m.scopes))
	shown := make(map[string]bool, len(m.scopes))
	for _, scope := range m.scopes {
		if scope.kind != "inspection" && !shown[scope.parentKey] {
			continue
		}
		visible = append(visible, scope)
		shown[scope.key] = m.scopeIsExpanded(scope)
	}
	return visible
}

func (m Model) scopeTreeIndex(visible []workspaceScope) int {
	for i, scope := range visible {
		if scope.key == m.scopeTreeKey {
			return i
		}
	}
	return 0
}

func (m *Model) setScopeTreeKey(key string) {
	if key != m.scopeTreeKey {
		m.compScroll, m.compScrollPause = 0, 2
	}
	m.scopeTreeKey = key
}

// syncScopeCursor reveals the active selection without expanding other packages.
// The persistent tree cursor stays independent of the searchable picker cursor.
func (m *Model) syncScopeCursor() {
	scope, found := m.workspaceScope(m.currentChart)
	if !found {
		if len(m.scopes) > 0 {
			m.setScopeTreeKey(m.scopes[0].key)
		}
		return
	}
	m.setScopeTreeKey(scope.key)
	for scope.parentKey != "" {
		m.setScopeExpanded(scope.parentKey, true)
		parent, found := m.workspaceScope(scope.parentKey)
		if !found {
			break
		}
		scope = parent
	}
}

func (m *Model) moveScopeCursor(delta int) {
	visible := m.visibleScopeTree()
	if len(visible) == 0 {
		return
	}
	index := clamp(m.scopeTreeIndex(visible)+delta, 0, len(visible)-1)
	m.setScopeTreeKey(visible[index].key)
}

func (m *Model) moveScopeBoundary(last bool) {
	visible := m.visibleScopeTree()
	if len(visible) == 0 {
		return
	}
	index := 0
	if last {
		index = len(visible) - 1
	}
	m.setScopeTreeKey(visible[index].key)
}

func (m *Model) scopeTreeLeft() {
	scope, found := m.workspaceScope(m.scopeTreeKey)
	if !found {
		return
	}
	if m.scopeHasChildren(scope) && m.scopeIsExpanded(scope) {
		m.setScopeExpanded(scope.key, false)
	} else if scope.parentKey != "" {
		m.setScopeTreeKey(scope.parentKey)
	}
}

func (m *Model) scopeTreeRight() {
	scope, found := m.workspaceScope(m.scopeTreeKey)
	if !found || !m.scopeHasChildren(scope) {
		return
	}
	if !m.scopeIsExpanded(scope) {
		m.setScopeExpanded(scope.key, true)
		return
	}
	for _, child := range m.scopes {
		if child.parentKey == scope.key && child.kind != "inspection" {
			m.setScopeTreeKey(child.key)
			return
		}
	}
}

func (m *Model) applyScopeCursor() bool {
	if _, found := m.workspaceScope(m.scopeTreeKey); !found {
		return false
	}
	m.currentChart = m.scopeTreeKey
	m.showOverview = false
	m.mapping, m.detailRef = nil, nil
	m.mappingRowDetail = false
	m.traceHistory = nil
	m.resetDetailPanels()
	m.updateFilteredValues()
	m.cursor, m.viewportOffset = 0, 0
	m.detailCursor, m.detailScrollOffset = 0, 0
	m.focusPane = FocusValues
	m.syncScopeCursor()
	m.syncInspectorSelection()
	m.setStatus("Scope: " + m.scopeLabel())
	return true
}

func (m Model) scopeTreePane(width, height int) string {
	focused := m.focusPane == FocusComponents
	inner := max(1, width-4)
	visible := m.visibleScopeTree()
	index := m.scopeTreeIndex(visible)
	rows := max(1, height-4)
	start := max(0, index-rows+1)
	lines := []string{paneHeadingStyle.Render("Scope")}
	for i := start; i < len(visible) && i < start+rows; i++ {
		scope := visible[i]
		prefix, label, labelWidth := m.scopeLabelCell(scope, width)
		name := truncateCell(label, labelWidth)
		if focused && i == index {
			name = scrollRowCell(label, m.compScroll, labelWidth)
		}
		line := truncateCell(prefix+name, inner)
		if focused && i == index {
			line = selectedRowStyle.Width(inner).Render(line)
		} else if scope.key == m.currentChart {
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	if len(visible) == 0 {
		lines = append(lines, helpStyle.Render("No scopes"))
	}
	lines = append(lines, helpStyle.Render(truncateCell("←/→ fold  enter select", inner)))
	return paneFrame(width, height, focused).Render(strings.Join(lines, "\n"))
}
