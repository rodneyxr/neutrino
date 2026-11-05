package tui

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	wideLayoutWidth   = 120
	mediumLayoutWidth = 80
)

type FocusPane int

const (
	FocusComponents FocusPane = iota
	FocusValues
	FocusInspector
	FocusMapping
)

type layoutMode int

const (
	layoutCompact layoutMode = iota
	layoutMedium
	layoutWide
)

type commandID int

const (
	commandSearch commandID = iota
	commandComponents
	commandNextComponent
	commandPrevComponent
	commandZarfSummary
	commandUDSSummary
	commandBundleMode
	commandSchema
	commandOpenSource
	commandCopyPath
	commandHelp
	commandQuit
	commandOverview
	commandAllInputs
	commandChanged
	commandUnresolved
	commandPackageSchema
)

type commandDefinition struct {
	id          commandID
	name        string
	key         string
	description string
}

var commands = [...]commandDefinition{
	{commandOverview, "Inspection overview", "0", "Review configuration flow and attention counts"},
	{commandAllInputs, "All configuration inputs", "8", "Browse every owner in the selected scope"},
	{commandChanged, "Changed inputs", "c", "Find inputs changed by a later source"},
	{commandUnresolved, "Unresolved inputs", "u", "Find runtime values and missing input evidence"},
	{commandSearch, "Filter values", "/", "Search the selected setting type and scope"},
	{commandComponents, "Select package or chart", "f", "Navigate the package, component and chart hierarchy"},
	{commandNextComponent, "Next component", "n / ]", "Jump to the next component"},
	{commandPrevComponent, "Previous component", "N / [", "Jump to the previous component"},
	{commandZarfSummary, "Toggle package variables", "Z", "Show the package variable summary"},
	{commandUDSSummary, "Toggle bundle overrides", "U", "Show the cross-package override summary"},
	{commandBundleMode, "Toggle authoring snippets", "b", "Show Package Values and UDS authoring snippets in Details"},
	{commandSchema, "View value schema", "s", "Open schema annotations in a separate view"},
	{commandPackageSchema, "View package schema", "S", "Inspect the selected package schema without adding schema rows"},
	{commandOpenSource, "View sources", "o", "Browse observed sources before opening an editor"},
	{commandCopyPath, "Copy value path", "y", "Copy the selected path through terminal OSC 52"},
	{commandHelp, "Show keyboard help", "?", "Open the context-sensitive shortcut reference"},
	{commandQuit, "Quit Neutrino", "q", "Close the interactive workspace"},
}

type pathScrollTickMsg time.Time

func tickPathScroll() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return pathScrollTickMsg(t)
	})
}
func (m *Model) setStatus(message string) {
	m.statusMessage = message
	m.statusExpiresAt = time.Now().Add(2 * time.Second)
}

func (m Model) layout() layoutMode {
	switch {
	case m.windowWidth >= wideLayoutWidth:
		return layoutWide
	case m.windowWidth >= mediumLayoutWidth:
		return layoutMedium
	default:
		return layoutCompact
	}
}

func (m *Model) moveFocus(delta int) {
	if m.focusPane == FocusComponents && m.layout() != layoutWide {
		m.focusPane = m.centralFocus()
		return
	}
	panes := []FocusPane{m.centralFocus(), FocusInspector}
	if m.layout() == layoutWide {
		panes = append([]FocusPane{FocusComponents}, panes...)
	}
	at := 0
	for i, pane := range panes {
		if pane == m.focusPane {
			at = i
			break
		}
	}
	at = (at + delta + len(panes)) % len(panes)
	if panes[at] == FocusComponents && m.focusPane != FocusComponents {
		m.scopeReturnFocus = m.focusPane
	}
	m.focusPane = panes[at]
	if m.focusPane == FocusInspector && m.mapping != nil {
		m.mappingRowDetail = true
	}
	m.statusMessage = ""
	m.buildDetailItems()
	if m.focusPane == FocusInspector {
		m.ensureDetailCursorVisible()
	}
}

func (m *Model) moveWithinFocusedPane(delta int) {
	switch m.focusPane {
	case FocusComponents:
		m.moveScopeCursor(delta)
	case FocusValues:
		prevVal := m.cursor
		m.cursor = clamp(m.cursor+delta, 0, len(m.filteredValues)-1)
		if m.cursor != prevVal {
			m.resetSelectedRowScroll()
		}
		m.ensureCursorVisible()
		m.syncInspectorSelection()
	case FocusMapping:
		m.moveMappingCursor(delta)
	case FocusInspector:
		previous := m.detailCursor
		m.detailCursor = clamp(m.detailCursor+delta, 0, len(m.detailItems)-1)
		m.detailScrollOffset = clamp(m.detailScrollOffset+delta, 0, m.detailScrollLimit())
		// At the first/last action the remaining evidence can extend beyond
		// that action's heading. Let the viewport reach it without snapping
		// back to an unchanged cursor, especially on short terminals.
		if m.detailCursor != previous {
			m.ensureDetailCursorVisible()
		}
	}
}

func (m *Model) moveFocusedToBoundary(last bool) {
	target := 0
	switch m.focusPane {
	case FocusComponents:
		m.moveScopeBoundary(last)
	case FocusValues:
		prevVal := m.cursor
		if last {
			target = len(m.filteredValues) - 1
		}
		m.cursor = clamp(target, 0, len(m.filteredValues)-1)
		if m.cursor != prevVal {
			m.resetSelectedRowScroll()
		}
		m.ensureCursorVisible()
		m.syncInspectorSelection()
	case FocusMapping:
		m.moveMappingBoundary(last)
	case FocusInspector:
		if last {
			target = len(m.detailItems) - 1
			m.detailScrollOffset = m.detailScrollLimit()
		} else {
			m.detailScrollOffset = 0
		}
		m.detailCursor = clamp(target, 0, len(m.detailItems)-1)
	}
}

func (m *Model) jumpComponent(delta int) {
	switch m.focusPane {
	case FocusComponents:
		m.moveScopeCursor(delta)
	case FocusValues:
		if len(m.filteredValues) == 0 {
			return
		}
		if delta > 0 {
			curr := m.settingGroupKey(m.filteredValues[m.cursor])
			for i := m.cursor + 1; i < len(m.filteredValues); i++ {
				if m.settingGroupKey(m.filteredValues[i]) != curr {
					m.cursor = i
					m.resetSelectedRowScroll()
					m.ensureCursorVisible()
					m.syncInspectorSelection()
					return
				}
			}
		} else {
			curr := m.settingGroupKey(m.filteredValues[m.cursor])
			startIdx := m.cursor
			for startIdx > 0 && m.settingGroupKey(m.filteredValues[startIdx-1]) == curr {
				startIdx--
			}
			if startIdx == m.cursor && startIdx > 0 {
				prevIdx := startIdx - 1
				prevComp := m.settingGroupKey(m.filteredValues[prevIdx])
				for prevIdx > 0 && m.settingGroupKey(m.filteredValues[prevIdx-1]) == prevComp {
					prevIdx--
				}
				m.cursor = prevIdx
			} else {
				m.cursor = startIdx
			}
			m.resetSelectedRowScroll()
			m.ensureCursorVisible()
			m.syncInspectorSelection()
		}
	}
}

func (m *Model) activateFocused() tea.Cmd {
	switch m.focusPane {
	case FocusComponents:
		m.applyScopeCursor()
	case FocusValues:
		if _, ok := m.selectedValue(); !ok {
			return nil
		}
		m.syncInspectorSelection()
		if m.detailRef != nil {
			m.resetInspectorRoute()
		}
		m.focusPane = FocusInspector
		m.statusMessage = ""
		m.buildDetailItems()
		m.ensureDetailCursorVisible()
	case FocusMapping:
		if m.mapping != nil && m.mapping.cursor < len(m.mapping.rows) {
			m.syncInspectorSelection()
			if m.detailRef != nil {
				m.resetInspectorRoute()
			}
			m.mappingRowDetail = true
			m.statusMessage = ""
			m.buildDetailItems()
			m.focusPane = FocusInspector
			m.ensureDetailCursorVisible()
		}
	case FocusInspector:
		if m.detailCursor < 0 || m.detailCursor >= len(m.detailItems) {
			return nil
		}
		detail := m.detailItems[m.detailCursor]
		if detail.Kind == DetailItemPanel {
			m.openDetailPanel(detail.panel, nil)
			return nil
		}
		if detail.Kind == DetailItemSource {
			m.openDetailPanel(detailSourceFile, &detail)
			return nil
		}
		if detail.Kind == DetailItemPreview {
			m.openDetailPanel(detailInputPreview, &detail)
			return nil
		}
		if detail.Kind == DetailItemMapping {
			value, _ := m.detailValue()
			m.openMapping(detail.mappingRef, FocusInspector)
			if value.IsPackageValue {
				for _, route := range value.Routes {
					if route.mapping == detail.mappingRef {
						m.focusMappingPath(route.Target)
						break
					}
				}
			}
			return nil
		}
		if detail.Kind == DetailItemLocation {
			return openEditor(detail.Location.File, detail.Location.Line, detail.Location.Column)
		}
		if detail.Kind == DetailItemFile || detail.Kind == DetailItemTrace && detail.File != "" {
			return openEditor(detail.File, max(1, detail.Location.Line), max(1, detail.Location.Column))
		}
		if m.followTrace(detail) {
			return nil
		}
		if detail.relatedValue != nil {
			detail.Kind = DetailItemTrace
			m.followTrace(detail)
		}
	}
	return nil
}

func (m *Model) beginFilter() {
	m.mapping = nil
	m.mappingRowDetail = false
	m.focusPane = FocusValues
	m.previousFilter = m.filterInput.Value()
	m.showOverview = false
	m.updateFilteredValues()
	m.inputMode = InputModeFilter
	m.filterInput.Focus()
	m.setStatus("Search " + m.selectedSettingTypeLabel())
}

func (m *Model) beginComponentFilter() {
	m.inputMode = InputModeChartSelect
	m.previousChart = m.currentChart
	m.chartQuery = ""
	m.chartInput.SetValue("")
	m.chartInput.Focus()
	m.updateFilteredValues()
	m.setStatus("Type to select a component")
}

func (m *Model) beginCommandPalette() {
	m.inputMode = InputModeCommand
	m.commandCursor = 0
	m.commandInput.SetValue("")
	m.commandInput.Focus()
}

func commandMatches(command commandDefinition, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(command.name+" "+command.description+" "+command.key), query)
}

func commandMatchCount(query string) int {
	count := 0
	for _, command := range commands {
		if commandMatches(command, query) {
			count++
		}
	}
	return count
}

func commandAt(query string, index int) (commandDefinition, bool) {
	match := 0
	for _, command := range commands {
		if !commandMatches(command, query) {
			continue
		}
		if match == index {
			return command, true
		}
		match++
	}
	return commandDefinition{}, false
}

func (m *Model) executeCommand(command commandDefinition) tea.Cmd {
	m.inputMode = InputModeNormal
	m.commandInput.Blur()
	m.commandInput.SetValue("")

	switch command.id {
	case commandOverview:
		m.showOverview = true
		m.overviewOffset = 0
		m.focusPane = m.centralFocus()
	case commandAllInputs:
		m.selectSettingType(7)
	case commandChanged:
		m.focusStatus("changed")
	case commandUnresolved:
		m.focusStatus("unresolved")
	case commandSearch:
		m.beginFilter()
	case commandComponents:
		m.beginScopePicker()
	case commandNextComponent:
		m.jumpComponent(1)
	case commandPrevComponent:
		m.jumpComponent(-1)
	case commandZarfSummary:
		m.toggleZarfSummary()
	case commandUDSSummary:
		m.toggleUDSSummary()
	case commandBundleMode:
		if m.focusPane == FocusInspector {
			m.openDetailPanel(detailAuthoring, nil)
		}
	case commandSchema:
		value, ok := m.detailValue()
		if m.focusPane == FocusInspector && ok && !runtimeReference(value) && m.selectedValueSchema(value) != nil {
			m.openDetailPanel(detailSchema, nil)
		}
	case commandPackageSchema:
		m.openPackageSchema()
	case commandOpenSource:
		return m.openSelectedSource()
	case commandCopyPath:
		return m.copySelectedPath()
	case commandHelp:
		m.showHelp = true
		m.helpOffset = 0
	case commandQuit:
		return tea.Quit
	}
	return nil
}

func (m *Model) toggleZarfSummary() {
	m.showZarfSummary = !m.showZarfSummary
	m.updateFilteredValues()
	m.cursor = 0
	m.viewportOffset = 0
	m.syncInspectorSelection()
	m.setStatus(toggleStatus("Package variable summary", m.showZarfSummary))
}

func (m *Model) toggleUDSSummary() {
	m.showUDSSummary = !m.showUDSSummary
	m.updateFilteredValues()
	m.cursor = 0
	m.viewportOffset = 0
	m.syncInspectorSelection()
	m.setStatus(toggleStatus("Bundle override summary", m.showUDSSummary))
}

func toggleStatus(label string, enabled bool) string {
	if enabled {
		return label + " on"
	}
	return label + " off"
}

func (m Model) selectedValue() (DisplayItem, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filteredValues) || m.filteredValues[m.cursor].IsEmptyState {
		return DisplayItem{}, false
	}
	return m.filteredValues[m.cursor], true
}

func (m *Model) openSelectedSource() tea.Cmd {
	value, ok := m.focusedActionValue()
	if !ok {
		m.setStatus("No source location for the selected value")
		return nil
	}
	if len(m.inspectorSources(value)) > 0 {
		if m.focusPane != FocusInspector {
			m.focusPane = FocusInspector
			m.resetInspectorRoute()
			m.buildDetailItems()
		}
		m.openDetailPanel(detailSources, nil)
		return nil
	}
	m.setStatus("No source location for the selected value")
	return nil
}

func (m *Model) copySelectedPath() tea.Cmd {
	value, ok := m.focusedActionValue()
	if !ok {
		m.setStatus("No value path to copy")
		return nil
	}
	path := value.Path
	if value.IsUDSOverrideVar && value.UDSVarPath != "" {
		path = value.UDSVarPath
	}
	m.setStatus("Copied " + path)
	return osc52Copy(path)
}

func clamp(value, low, high int) int {
	if high < low {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (m Model) workspaceView() string {
	width := m.windowWidth
	height := m.windowHeight
	if width < 1 {
		width = 100
	}
	if height < 1 {
		height = 30
	}

	if m.inputMode == InputModeScopeSelect {
		return m.scopePicker(width, height)
	}
	if m.showHelp {
		return m.helpView(width, height)
	}
	if m.inputMode == InputModeCommand {
		return m.commandPaletteView(width, height)
	}

	header := m.workspaceHeader(width)
	footer := m.workspaceFooter(width)
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 4 {
		bodyHeight = 4
	}

	var body string
	if m.showOverview {
		body = m.overviewPane(width, bodyHeight)
	} else {
		body = m.persistentWorkspace(width, bodyHeight)
	}

	body = fitWorkspace(body, width, bodyHeight)
	return header + "\n" + body + "\n" + footer
}

func (m Model) workspaceHeader(width int) string {
	chart := terminalText(m.scopeLabel())
	left := titleStyle.Render("◇ NEUTRINO") + "  " + helpStyle.Render("f Scope · ") + headerStyle.Render(chart)
	panes := "2 panes"
	if width < mediumLayoutWidth {
		panes = "single pane"
	} else if m.workspaceGeometry(width).scope > 0 {
		panes = "3 panes"
	}
	right := fmt.Sprintf("%d settings  •  %s", len(m.filteredValues), panes)
	if !m.showOverview {
		right = fmt.Sprintf("%d settings · %s", len(m.filteredValues), m.selectedSettingTypeLabel())
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return headerBarStyle.Width(width).Render(truncateCell(left, width-2))
	}
	return headerBarStyle.Width(width).Render(left + strings.Repeat(" ", gap) + helpStyle.Render(right))
}

func layoutLabel(mode layoutMode) string {
	if mode == layoutCompact {
		return "single pane"
	}
	if mode == layoutWide {
		return "3 panes"
	}
	return "2 panes"
}
func (m Model) workspaceFooter(width int) string {
	if width < 60 && m.inputMode == InputModeNormal {
		action := "enter view"
		switch {
		case m.showOverview:
			action = "8 browse"
		case m.focusPane == FocusComponents:
			action = "enter apply"
		case m.focusPane == FocusInspector:
			action = "enter open"
			if len(m.detailItems) == 0 {
				action = "j/k scroll"
			}
		}
		controls := "esc back  ? help  q quit"
		if m.statusMessage != "" {
			action = truncateCell(m.statusMessage, max(1, width-lipgloss.Width(controls)-4))
		}
		line := action + "  " + controls
		if lipgloss.Width(line) > width-2 {
			line = action + "  esc back  ? help"
		}
		return footerBarStyle.Width(width).Render(truncateCell(line, width-2))
	}
	var primary string
	switch m.inputMode {
	case InputModeFilter:
		primary = m.filterInput.View() + helpStyle.Render("  enter apply  esc cancel")
	case InputModeChartSelect:
		primary = m.chartInput.View() + helpStyle.Render("  ↑↓ choose  enter apply  esc cancel")
	default:
		switch {
		case m.showOverview:
			primary = helpStyle.Render("j/k scroll  8 browse  f scope  ? help  q quit")
		case m.focusPane == FocusComponents:
			primary = helpStyle.Render("↑↓ scope  ← parent  → expand  enter apply  S schema  tab settings  ? help")
		case m.focusPane == FocusMapping:
			primary = helpStyle.Render("j/k select  enter inspector  tab panes  f scope  esc back  ? help")
		case m.focusPane == FocusInspector:
			primary = helpStyle.Render(m.inspectorFooter(width))
		default:
			primary = helpStyle.Render("←/→ type  8 all  j/k select  enter inspect  / search  f scope  tab panes  ? help")
		}
	}
	if m.inputMode == InputModeNormal && m.statusMessage != "" {
		hint := helpStyle.Render("esc back  ? help  q quit")
		gap := width - lipgloss.Width(m.statusMessage) - lipgloss.Width(hint) - 2
		if gap < 1 {
			return footerBarStyle.Width(width).Render(truncateCell(m.statusMessage, width-lipgloss.Width(hint)-3) + " " + hint)
		}
		return footerBarStyle.Width(width).Render(m.statusMessage + strings.Repeat(" ", gap) + hint)
	}
	if width < 100 && m.inputMode == InputModeNormal && m.statusMessage == "" {
		switch {
		case m.showOverview:
			primary = helpStyle.Render("j/k scroll  8 browse  esc back  ? help  q quit")
		case m.focusPane == FocusComponents:
			primary = helpStyle.Render("←/→ fold  enter apply  tab panes  esc back  ? help  q quit")
		case m.focusPane == FocusMapping:
			primary = helpStyle.Render("enter inspector  tab panes  f scope  esc back  ? help  q quit")
		case m.focusPane == FocusInspector:
			primary = helpStyle.Render("enter open  tab panes  esc back  ? help  q quit")
		default:
			primary = helpStyle.Render("←/→ type  8 all  enter inspect  / search  f scope  ? help")
		}
	}
	line := primary
	if m.statusMessage != "" {
		remaining := width - lipgloss.Width(primary) - lipgloss.Width(m.statusMessage) - 3
		if remaining > 0 {
			line += strings.Repeat(" ", remaining) + m.statusMessage
		}
	}
	return footerBarStyle.Width(width).Render(truncateCell(line, width-2))
}

func paneFrame(width, height int, focused bool) lipgloss.Style {
	style := paneStyle
	if focused {
		style = focusedPaneStyle
	}
	return style.Width(max(1, width-2)).Height(max(1, height-2))
}

func (m Model) componentPane(width, height int) string {
	contentHeight := max(1, height-4)
	start := 0
	if m.componentCursor >= contentHeight {
		start = m.componentCursor - contentHeight + 1
	}
	var content strings.Builder
	content.WriteString(paneHeadingStyle.Render("COMPONENTS"))
	content.WriteByte('\n')
	for index := start; index < len(m.chartNames) && index < start+contentHeight; index++ {
		name := m.chartNames[index]
		displayName := m.componentDisplayName(name, width)
		prefix := "  "
		if name == m.currentChart {
			prefix = "● "
		}
		if m.focusPane == FocusComponents && index == m.componentCursor {
			if name == m.currentChart {
				prefix = "> ● "
			} else {
				prefix = "> "
			}
			maxVisible := max(1, width-4-lipgloss.Width(prefix))
			nameCell := truncateCell(displayName, maxVisible)
			if m.compScroll > 0 {
				nameCell = scrollCell(displayName, m.compScroll, maxVisible)
			}
			content.WriteString(selectedRowStyle.Render(truncateCell(prefix+nameCell, width-4)))
		} else {
			content.WriteString(truncateCell(prefix+displayName, width-4))
		}
		content.WriteByte('\n')
	}
	content.WriteByte('\n')
	content.WriteString(helpStyle.Render(fmt.Sprintf("Zarf summary %s\nUDS summary  %s", onOff(m.showZarfSummary), onOff(m.showUDSSummary))))
	return paneFrame(width, height, m.focusPane == FocusComponents).Render(content.String())
}

func (m Model) componentLabel(name string) string {
	if name == "All Charts" {
		return "All components"
	}
	if name == "" {
		if component := m.components[name]; component != nil && component.ChartName != "" {
			return component.ChartName
		}
		return "Standalone chart"
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return name
}

func (m Model) componentDisplayName(name string, paneWidth int) string {
	if name == "All Charts" {
		return "All components"
	}
	if name == "" {
		if component := m.components[name]; component != nil && component.ChartName != "" {
			return component.ChartName
		}
		return "Standalone chart"
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 && paneWidth < 36 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return name
}

func (m Model) componentWidth(totalWidth int) int {
	focused := m.focusPane == FocusComponents
	switch m.layout() {
	case layoutWide:
		if focused {
			return min(52, max(42, totalWidth*38/100))
		}
		return 24
	case layoutMedium:
		if focused {
			return min(44, max(36, totalWidth*45/100))
		}
		return 22
	default:
		return totalWidth
	}
}

func (m Model) columnWidths(innerWidth int) (pathWidth, valWidth, typeWidth, srcWidth int, showMetadata bool) {
	if innerWidth < 52 {
		pathWidth = max(8, innerWidth*58/100)
		return pathWidth, max(1, innerWidth-pathWidth-3), 0, 0, false
	}
	typeWidth = 10
	valWidth = clamp(innerWidth/3, 10, 20)
	pathWidth = max(8, innerWidth-valWidth-typeWidth-4)
	return pathWidth, valWidth, typeWidth, 0, true
}

func (m Model) currentPathWidth() int {
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	valuesWidth := m.workspaceGeometry(width).settings
	innerWidth := max(1, valuesWidth-4)
	pathWidth, _, _, _, _ := m.columnWidths(innerWidth)
	return pathWidth
}

func (m *Model) stepAutoScroll() {
	if m.inputMode != InputModeNormal || m.showHelp || m.showOverview {
		return
	}
	if m.focusPane == FocusComponents {
		m.advanceScopeLabelScroll()
	} else if m.focusPane == FocusValues || m.focusPane == FocusMapping {
		m.advanceSelectedRowScroll()
	}
}

func (m *Model) stepComponentScroll() { m.advanceScopeLabelScroll() }

func (m *Model) stepValuePathScroll() { m.advanceSelectedRowScroll() }
func (m *Model) stepPathScroll() {
	m.stepValuePathScroll()
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func (m Model) valuesPane(width, height int) string {
	inner := max(1, width-4)
	var content strings.Builder
	// Sanitize and size untrusted text before applying our own styles. Stripping
	// ANSI after rendering would also erase the selected-row highlight.
	add := func(line string) { content.WriteString(line + "\n") }
	add(paneHeadingStyle.Render("Settings"))
	add(m.settingsTabs(inner))
	add(filterStyle.Render(m.settingsQueryLine(inner)))
	pathWidth, valWidth, typeWidth, _, metadata := m.columnWidths(inner)
	heading := fmt.Sprintf("  %-*s %-*s", pathWidth, "Setting", valWidth, "Value")
	if metadata {
		heading += " Kind"
	}
	add(tableHeaderStyle.Render(truncateCell(heading, inner)))
	available := max(2, height-8)
	used := 0
	lastOwner := ""
	start := clamp(m.viewportOffset, 0, max(0, len(m.filteredValues)-1))
	for index := start; index < len(m.filteredValues) && used < available; index++ {
		value := m.filteredValues[index]
		owner := m.settingGroupKey(value)
		if index == start || owner != lastOwner {
			if used+2 > available {
				break
			}
			add(groupHeadingStyle.Render(componentBand(m.settingGroupLabel(value), inner)))
			used++
			lastOwner = owner
		}
		cells := m.settingRowCells(value, inner)
		path := scrollRowCell(cells.path, 0, pathWidth)
		cellValue := scrollRowCell(cells.value, 0, valWidth)
		if index == m.cursor && m.focusPane == FocusValues {
			path = scrollRowCell(cells.path, m.pathScroll, pathWidth)
			cellValue = scrollRowCell(cells.value, m.valueScroll, valWidth)
		}
		line := padRowCell(path, pathWidth) + " " + padRowCell(cellValue, valWidth)
		if metadata {
			line += " " + padRowCell(truncateCell(displayKind(value), typeWidth), typeWidth)
		}
		marker := "  "
		if index == m.cursor {
			marker = "> "
		}
		line = truncateCell(marker+line, inner)
		if index == m.cursor {
			add(selectedRowStyle.Render(line))
		} else if value.settingChildren > 0 {
			add(groupHeadingStyle.Render(line))
		} else {
			add(kindStyleFor(value).Render(line))
		}
		used++
	}
	if len(m.filteredValues) == 0 {
		typeName := "settings"
		if !m.allCategories {
			typeName = settingTabLabels[m.category]
		}
		message := "No " + typeName + " in this scope"
		if strings.TrimSpace(m.filterInput.Value()) != "" {
			message = "No matching " + typeName
		}
		add(helpStyle.Render(truncateCell(message, inner)))
		if !m.allCategories && available > 1 {
			add(helpStyle.Render(truncateCell("8 All types · / change search", inner)))
		}
	}
	return paneFrame(width, height, m.focusPane == FocusValues).Render(strings.TrimSuffix(content.String(), "\n"))
}

// The package band carries the owner; keep the full path on DisplayItem for
// searching, Details, and copying.
func (m Model) valueListPath(value DisplayItem) string {
	if runtimeReference(value) && inspectorAbsolutePath(value.Path) {
		return m.referenceDisplayPath(value)
	}
	if value.settingLabel != "" {
		return strings.Repeat("  ", value.settingDepth) + value.settingLabel
	}
	if value.IsPackageValue && value.Component == "UDS Next bundle" && len(value.ValueFiles) > 0 {
		return value.PackageName + " · " + strings.Join(value.ValueFiles, ", ")
	}
	if m.nextRail && (value.IsPkgVar || value.IsZarfVar) && value.PackageName != "" {
		return strings.TrimPrefix(value.Path, value.PackageName+".")
	}
	return value.Path
}

func componentBand(label string, width int) string {
	prefix := "━━ "
	if width <= lipgloss.Width(prefix) {
		return truncateCell(prefix, width)
	}
	title := prefix + truncateCell(label, width-lipgloss.Width(prefix))
	if remaining := width - lipgloss.Width(title); remaining > 1 {
		title += " " + strings.Repeat("━", remaining-1)
	}
	return title
}

func (m Model) valueLineCount(start, end int) int {
	lines := 0
	owner := ""
	for i := start; i <= end && i < len(m.filteredValues); i++ {
		next := m.settingGroupKey(m.filteredValues[i])
		if i == start || next != owner {
			lines++
			owner = next
		}
		lines++
	}
	return lines
}

func settingsEntryCount(count int) string {
	if count == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", count)
}

func displayValue(value DisplayItem) string {
	if value.settingChildren > 0 {
		return settingsEntryCount(value.settingChildren)
	}
	if value.IsPackageMapping {
		if runtimeReference(value) {
			if referenceRequiresDeployment(value) {
				return "deployment context"
			}
			return "not evaluated"
		}
		return "mapping"
	}
	if value.IsPackageValue && value.Component == "UDS Next bundle" {
		if len(value.ValueFiles) == 0 {
			return "package"
		}
		if len(value.ValueFiles) == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", len(value.ValueFiles))
	}
	if value.IsPackageValue && !value.ValuePresent {
		if value.Schema != nil {
			return "not supplied"
		}
		return "files / schema"
	}
	if value.IsBundleVariable && !value.ValuePresent {
		return "not supplied"
	}
	if value.Wildcard {
		return "dynamic path"
	}
	if value.IsUDSOverrideValue {
		return formatDefaultValue(value.OverrideValue)
	}
	if value.DefaultPresent {
		return mappingCell(value.DefaultValue, true)
	}
	if value.Schema != nil && value.DefaultValue == nil {
		return "not supplied"
	}
	return formatDefaultValue(value.DefaultValue)
}

func displayKind(value DisplayItem) string {
	if value.settingChildren > 0 {
		return "group"
	}
	switch {
	case value.IsBundleVariable:
		return "variable"
	case value.IsPackageMapping:
		if runtimeReference(value) {
			if referenceRequiresDeployment(value) {
				return "runtime use"
			}
			return "template use"
		}
		return "declared"
	case value.IsPackageValue:
		if value.Component == "UDS Next bundle" {
			return "files"
		}
		if !value.ValuePresent && value.Schema != nil {
			return "schema"
		}
		if value.MappingStatus != "" {
			return value.MappingStatus
		}
		return "package"
	case value.IsPkgVar || value.IsZarfVar:
		return "variable"
	case value.IsUDSOverrideValue:
		return "fixed"
	case value.IsUDSOverrideVar:
		return "variable"
	case value.IsEmptyState:
		return "empty"
	case value.Wildcard:
		return "dynamic"
	case value.Schema != nil && !value.DefaultPresent && value.DefaultValue == nil:
		return "schema"
	case len(value.Locations) > 0:
		return "field"
	default:
		return "inferred"
	}
}

func kindStyleFor(value DisplayItem) lipgloss.Style {
	switch {
	case value.IsPackageValue:
		switch value.MappingStatus {
		case "mapped":
			return mappedValueStyle
		case "excluded":
			return excludedValueStyle
		case "unmapped":
			return helpStyle
		case "mixed", "runtime":
			return zarfVarStyle
		}
	case value.IsPkgVar || value.IsZarfVar:
		return zarfVarStyle
	case value.IsUDSOverrideValue:
		return udsOverrideValueStyle
	case value.IsUDSOverrideVar:
		return udsOverrideVarStyle
	case value.IsEmptyState:
		return helpStyle
	case value.Wildcard:
		return zarfVarStyle
	}
	return codeStyle
}

func (m Model) inspectorPane(width, height int) string {
	focused := m.focusPane == FocusInspector
	inner := max(1, width-4)
	heading := paneHeadingStyle.Render("Inspector")
	frame := paneFrame(width, height, focused)
	if _, ok := m.detailValue(); !ok {
		return frame.Render(heading + "\n" + helpStyle.Render("Select a setting."))
	}
	content, _ := m.inspectorContent(inner, focused)
	visible := max(1, height-4)
	total := strings.Count(content, "\n") + 1
	if total <= height-3 {
		return frame.Render(heading + "\n" + content)
	}
	offset := clamp(m.detailScrollOffset, 0, max(0, total-visible))
	body := clipLinesAt(content, visible, offset)
	position := fmt.Sprintf("%d–%d / %d", offset+1, min(total, offset+visible), total)
	return frame.Render(heading + "\n" + body + "\n" + helpStyle.Render(truncateCell(position, inner)))
}

func (m Model) inspectorVisibleLines() int {
	width, height := m.windowWidth, m.windowHeight
	if width < 1 {
		width = 100
	}
	if height < 1 {
		height = 30
	}
	bodyHeight := height - lipgloss.Height(m.workspaceHeader(width)) - lipgloss.Height(m.workspaceFooter(width))
	return max(1, bodyHeight-4)
}

func (m Model) detailScrollLimit() int {
	content, _ := m.inspectorContent(max(1, m.inspectorWidth()-4), true)
	return max(0, strings.Count(content, "\n")+1-m.inspectorVisibleLines())
}

func (m *Model) ensureDetailCursorVisible() {
	if len(m.detailItems) == 0 {
		return
	}
	width := max(1, m.inspectorWidth()-4)
	_, row := m.inspectorContent(width, true)
	visible := m.inspectorVisibleLines()
	selectedHeight := 1
	if value, ok := m.detailValue(); ok {
		index := 0
		for _, line := range m.inspectorDocument(value).lines {
			if line.action == nil {
				continue
			}
			if index == m.detailCursor {
				selectedHeight = min(visible, strings.Count(wrapCell(line.text, max(1, width-2)), "\n")+1)
				break
			}
			index++
		}
	}
	if row < m.detailScrollOffset {
		m.detailScrollOffset = row
	} else if row+selectedHeight > m.detailScrollOffset+visible {
		m.detailScrollOffset = row + selectedHeight - visible
	}
	m.detailScrollOffset = clamp(m.detailScrollOffset, 0, m.detailScrollLimit())
}

// chartRelativeLocation changes only the display path; DetailItem keeps the
// original filename so Enter still opens the actual source file in the editor.
func (m Model) chartRelativeLocation(value DisplayItem, file string) string {
	key := value.Component
	if value.IsSummaryItem {
		key = value.SummaryComponent
	}
	return m.chartRelativeLocationFor(key, file)
}

func (m Model) chartRelativeLocationFor(key, file string) string {
	if filepath.IsAbs(file) {
		if result := m.components[key]; result != nil && result.Source != "" {
			if relative, err := filepath.Rel(result.Source, file); err == nil &&
				relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
				!filepath.IsAbs(relative) {
				return relative
			}
		}
		if at := strings.Index(file, string(filepath.Separator)+"templates"+string(filepath.Separator)); at >= 0 {
			return file[at+1:]
		}
		return filepath.Base(file)
	}
	file = filepath.Clean(file)
	if file == ".." || strings.HasPrefix(file, ".."+string(filepath.Separator)) {
		return filepath.Base(file)
	}
	return file
}

// tailCell preserves the filename and line when a source path exceeds the pane.
func tailCell(value string, width int) string {
	value = terminalText(value)
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width-1 {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func (m Model) commandPaletteView(width, height int) string {
	paletteWidth := min(100, max(20, width-4))
	contentWidth := max(1, paletteWidth-6)
	query := m.commandInput.Value()
	matchCount := commandMatchCount(query)
	var content strings.Builder
	content.WriteString(titleStyle.Render("COMMAND PALETTE") + "\n")
	content.WriteString(truncateCell(m.commandInput.View(), contentWidth) + "\n\n")
	content.WriteString(tableHeaderStyle.Render(truncateCell(fmt.Sprintf("%-8s %-28s %s", "KEY", "ACTION", "DESCRIPTION"), contentWidth)) + "\n")
	matchIndex := 0
	visible := max(1, height-9)
	start := max(0, m.commandCursor-visible+1)
	for _, command := range commands {
		if !commandMatches(command, query) {
			continue
		}
		if matchIndex < start || matchIndex >= start+visible {
			matchIndex++
			continue
		}
		line := fmt.Sprintf("%-8s %-28s %s", command.key, command.name, command.description)
		line = truncateCell(line, contentWidth-2)
		if matchIndex == m.commandCursor {
			content.WriteString(selectedRowStyle.Render("> " + truncateCell(line, contentWidth-2)))
		} else {
			content.WriteString("  " + truncateCell(line, contentWidth-2))
		}
		content.WriteByte('\n')
		matchIndex++
	}
	if matchCount == 0 {
		content.WriteString(helpStyle.Render(truncateCell("No matching commands.", contentWidth)) + "\n")
	}
	content.WriteString(helpStyle.Render(truncateCell("↑↓ move  enter  esc", contentWidth)))
	panel := modalStyle.Width(paletteWidth - 2).Render(content.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) helpView(width, height int) string {
	content := titleStyle.Render("KEYBOARD HELP") + "\n\n"
	content += "0 / 8             overview / all inputs\n" + "c / u             changed / unresolved filter\n"
	navigation := "next / previous owner group"
	space := "apply scope / Inspector authoring"
	enter := "apply scope / inspect / follow action"
	content += "1–7               setting type; 8 all\n1 Bundle vars · 2 Bundle files · 3 Zarf vars\n4 Zarf values · 5 Mappings & uses\n6 Helm values · 7 Legacy overrides\n"
	content += "Tab / Shift+Tab   Scope / Settings / Inspector\n" +
		"h / l             focus previous / next pane\n" +
		"← / →             Settings type / Scope fold\n" +
		"j / k, ↑ / ↓      move 1 line in focused pane\n" +
		"J / K             jump 5 lines in focused pane\n" +
		"n / N, ] / [     " + navigation + "\n" +
		"g / G             first / last item\n" +
		"Enter             " + enter + "\n" +
		"/                 search selected type and scope\n" +
		"f                 package / component / chart\n" +
		":                 command palette\n" +
		"Space             " + space + "\n" +
		"a                 toggle other paths in mapping\n" +
		"o                 view source locations\n" +
		"y                 copy value path\n" +
		"p                 copy selected mapping input path\n" +
		"v / t / s         full value / history / schema\n" +
		"S                 whole package schema\n" +
		"Z / U             summary toggles\n" +
		"b                 Package Values / UDS authoring\n" +
		"Esc               back / clear\n" +
		"q                 quit\n\n" +
		helpStyle.Render("Press ? or Esc to close")
	panelWidth := min(58, max(34, width-4))
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = truncateCell(line, max(1, panelWidth-6))
	}
	visible := max(1, height-7)
	body := clipLinesAt(strings.Join(lines, "\n"), visible, m.helpOffset)
	body += "\n" + helpStyle.Render(truncateCell("↑↓ scroll  ? / Esc close", panelWidth-6))
	panel := modalStyle.Width(panelWidth - 2).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func truncateCell(value string, width int) string {
	value = terminalText(value)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	var result strings.Builder
	used := 0
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		runeWidth := lipgloss.Width(string(r))
		if used+runeWidth > width-1 {
			break
		}
		result.WriteRune(r)
		used += runeWidth
		value = value[size:]
	}
	result.WriteRune('…')
	return result.String()
}

func scrollCell(value string, offset, width int) string {
	if offset <= 0 {
		return truncateCell(value, width)
	}
	runes := []rune(value)
	offset = clamp(offset, 0, max(0, len(runes)-1))
	if width <= 1 {
		return "‹"
	}
	return "‹" + truncateCell(string(runes[offset:]), width-1)
}

func wrapCell(value string, width int) string {
	value = terminalMultiline(value)
	if width < 1 {
		return ""
	}

	var wrapped strings.Builder
	wrapped.Grow(len(value) + len(value)/width)
	first := true
	writeLine := func(line string) {
		if !first {
			wrapped.WriteByte('\n')
		}
		wrapped.WriteString(line)
		first = false
	}

	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			writeLine("")
			continue
		}
		for len(line) > 0 {
			end, lineWidth := 0, 0
			for end < len(line) {
				r, size := utf8.DecodeRuneInString(line[end:])
				runeWidth := 1
				if r >= utf8.RuneSelf {
					runeWidth = lipgloss.Width(string(r))
				}
				if end > 0 && lineWidth+runeWidth > width {
					break
				}
				end += size
				lineWidth += runeWidth
				if lineWidth >= width {
					break
				}
			}
			writeLine(line[:end])
			line = line[end:]
		}
	}
	return wrapped.String()
}
func clipLinesAt(value string, height, offset int) string {
	lines := strings.Split(value, "\n")
	if height <= 0 || len(lines) == 0 {
		return ""
	}
	offset = clamp(offset, 0, max(0, len(lines)-height))
	end := min(len(lines), offset+height)
	return strings.Join(lines[offset:end], "\n")
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.inputMode = InputModeNormal
		m.filterInput.Blur()
		m.setStatus(fmt.Sprintf("%d matching values", len(m.filteredValues)))
	case "esc":
		m.inputMode = InputModeNormal
		m.filterInput.Blur()
		m.filterInput.SetValue(m.previousFilter)
		m.updateFilteredValues()
		m.cursor = 0
		m.viewportOffset = 0
		m.syncInspectorSelection()
		m.setStatus("Filter edit cancelled")
	default:
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.updateFilteredValues()
		m.ensureCursorVisible()
		m.buildDetailItems()
		return m, cmd
	}
	return m, nil
}

func (m *Model) resolveComponentQuery() {
	if m.currentChart == "" {
		m.currentChart = "All Charts"
		return
	}
	for _, name := range m.chartNames {
		if strings.EqualFold(name, m.currentChart) {
			m.currentChart = name
			return
		}
	}
	query := strings.ToLower(m.currentChart)
	for _, name := range m.chartNames {
		if strings.Contains(strings.ToLower(name), query) {
			m.currentChart = name
			return
		}
	}
}

func (m Model) handleComponentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.resolveComponentQuery()
		m.inputMode = InputModeNormal
		m.chartInput.Blur()
		m.chartInput.SetValue("")
		m.updateFilteredValues()
		m.cursor = 0
		m.viewportOffset = 0
		m.focusPane = FocusValues
		m.buildDetailItems()
		m.syncComponentCursor()
		m.setStatus("Showing " + m.componentLabel(m.currentChart))
	case "esc":
		m.inputMode = InputModeNormal
		m.chartInput.Blur()
		m.currentChart = m.previousChart
		m.chartInput.SetValue("")
		m.updateFilteredValues()
		m.ensureCursorVisible()
		m.syncComponentCursor()
		m.setStatus("Component selection cancelled")
	case "up", "down":
		m.cycleComponentMatch(msg.String() == "down")
	default:
		var cmd tea.Cmd
		m.chartInput, cmd = m.chartInput.Update(msg)
		m.chartQuery = m.chartInput.Value()
		m.currentChart = m.chartQuery
		if m.currentChart == "" {
			m.currentChart = "All Charts"
		}
		m.updateFilteredValues()
		m.ensureCursorVisible()
		return m, cmd
	}
	return m, nil
}

func (m *Model) cycleComponentMatch(forward bool) {
	query := strings.ToLower(m.chartQuery)
	matchCount := 0
	currentMatch := -1
	for _, name := range m.chartNames {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		if strings.EqualFold(name, m.chartInput.Value()) {
			currentMatch = matchCount
		}
		matchCount++
	}
	if matchCount == 0 {
		return
	}
	if forward {
		currentMatch++
	} else {
		currentMatch--
	}
	currentMatch = (currentMatch + matchCount) % matchCount
	match := 0
	for _, name := range m.chartNames {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		if match == currentMatch {
			m.chartInput.SetValue(name)
			m.currentChart = name
			break
		}
		match++
	}
	m.updateFilteredValues()
	m.ensureCursorVisible()
	m.syncComponentCursor()
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputMode = InputModeNormal
		m.commandInput.Blur()
		m.commandInput.SetValue("")
	case "up", "ctrl+k":
		m.commandCursor = clamp(m.commandCursor-1, 0, commandMatchCount(m.commandInput.Value())-1)
	case "down", "ctrl+j":
		m.commandCursor = clamp(m.commandCursor+1, 0, commandMatchCount(m.commandInput.Value())-1)
	case "enter":
		if command, ok := commandAt(m.commandInput.Value(), m.commandCursor); ok {
			cmd := m.executeCommand(command)
			return m, cmd
		}
	default:
		var cmd tea.Cmd
		m.commandInput, cmd = m.commandInput.Update(msg)
		m.commandCursor = 0
		return m, cmd
	}
	return m, nil
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
		m.selectSettingType(int(key[0] - '1'))
		return m, nil
	}
	if m.showOverview {
		switch msg.String() {
		case "j", "down":
			m.overviewOffset++
			return m, nil
		case "k", "up":
			m.overviewOffset = max(0, m.overviewOffset-1)
			return m, nil
		case "pgdown":
			m.overviewOffset += max(1, m.windowHeight-5)
			return m, nil
		case "pgup":
			m.overviewOffset = max(0, m.overviewOffset-max(1, m.windowHeight-5))
			return m, nil
		case "g":
			m.overviewOffset = 0
			return m, nil
		case "G":
			m.overviewOffset = 1000
			return m, nil
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.moveFocus(1)
	case "shift+tab":
		m.moveFocus(-1)
	case "h":
		m.moveFocus(-1)
	case "l":
		m.moveFocus(1)
	case "left":
		if m.focusPane == FocusComponents {
			m.scopeTreeLeft()
		} else if m.focusPane == FocusValues {
			m.selectSettingType(m.adjacentSettingType(-1))
		} else {
			m.moveFocus(-1)
		}
	case "right":
		if m.focusPane == FocusComponents {
			m.scopeTreeRight()
		} else if m.focusPane == FocusValues {
			m.selectSettingType(m.adjacentSettingType(1))
		} else {
			m.moveFocus(1)
		}
	case "j", "down":
		m.moveWithinFocusedPane(1)
	case "k", "up":
		m.moveWithinFocusedPane(-1)
	case "J":
		m.moveWithinFocusedPane(5)
	case "K":
		m.moveWithinFocusedPane(-5)
	case "n", "]":
		m.jumpComponent(1)
	case "N", "[":
		m.jumpComponent(-1)
	case "g":
		m.moveFocusedToBoundary(false)
	case "G":
		m.moveFocusedToBoundary(true)
	case "pgup":
		m.moveWithinFocusedPane(-max(5, m.windowHeight-7))
	case "pgdown":
		m.moveWithinFocusedPane(max(5, m.windowHeight-7))
	case "enter":
		if m.showOverview {
			m.showOverview = false
			m.focusPane = m.centralFocus()
			return m, nil
		}
		cmd := m.activateFocused()
		return m, cmd
	case "0":
		m.showOverview = !m.showOverview
		m.overviewOffset = 0
		m.focusPane = m.centralFocus()
	case "c":
		m.focusStatus("changed")
	case "u":
		m.focusStatus("unresolved")
	case "a":
		if m.focusPane == FocusMapping && m.mapping != nil {
			m.mapping.showOthers = !m.mapping.showOthers
			m.mapping.selectRows()
			m.ensureMappingCursorVisible()
			m.syncInspectorSelection()
		}
	case "/":
		m.beginFilter()
	case "f":
		m.beginScopePicker()
	case ":":
		m.beginCommandPalette()
	case "?":
		m.showHelp = true
		m.helpOffset = 0
	case "esc":
		if m.showOverview {
			m.showOverview = false
			return m, nil
		}
		if m.focusPane == FocusInspector && m.backDetailPanel() {
			return m, nil
		}
		if m.focusPane == FocusInspector && m.detailRef != nil && m.returnTrace() {
			return m, nil
		} else if m.focusPane == FocusInspector && m.detailRef != nil {
			m.detailRef = nil
			m.detailCursor, m.detailScrollOffset = 0, 0
			m.buildDetailItems()
		} else if m.focusPane == FocusInspector && m.mappingRowDetail {
			m.mappingRowDetail = false
			m.detailScrollOffset = 0
			m.focusPane = FocusMapping
		} else if m.focusPane == FocusMapping {
			screen := m.mapping
			m.focusPane = screen.origin
			m.mapping = screen.previous
			m.mappingRowDetail = screen.returnRowDetail
			m.detailRef = screen.returnDetailRef
			m.detailCursor = screen.returnDetailCursor
			m.detailScrollOffset = screen.returnDetailOffset
			m.detailPanel, m.detailPanelHistory, m.detailSource = screen.returnPanel, screen.returnPanels, screen.returnSource
			m.traceHistory = screen.returnTraceHistory
			m.inspectorSelection = m.liveSelectionKey()
			m.buildDetailItems()
		} else if m.focusPane == FocusInspector {
			m.focusPane = m.centralFocus()
			m.detailScrollOffset = 0
		} else if m.focusPane == FocusComponents {
			m.focusPane = m.scopeReturnFocus
			if m.focusPane != FocusInspector {
				m.focusPane = m.centralFocus()
			}
		} else if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.updateFilteredValues()
			m.cursor = 0
			m.viewportOffset = 0
			m.syncInspectorSelection()
			m.setStatus("Value filter cleared")
		}
	case " ", "space":
		switch m.focusPane {
		case FocusComponents:
			cmd := m.activateFocused()
			return m, cmd
		case FocusInspector:
			m.openDetailPanel(detailAuthoring, nil)
		}
	case "Z":
		m.toggleZarfSummary()
	case "U":
		m.toggleUDSSummary()
	case "b":
		if m.focusPane == FocusInspector {
			m.openDetailPanel(detailAuthoring, nil)
		}
	case "t":
		if value, ok := m.detailValue(); ok && m.focusPane == FocusInspector && m.hasValueHistory(value) {
			m.openDetailPanel(detailProvenance, nil)
		}
	case "v":
		if value, ok := m.detailValue(); ok && m.focusPane == FocusInspector && m.canViewFullValue(value) {
			m.openDetailPanel(detailFullValue, nil)
		}
	case "s":
		if m.focusPane == FocusInspector {
			value, ok := m.detailValue()
			if ok && !runtimeReference(value) && m.selectedValueSchema(value) != nil {
				m.openDetailPanel(detailSchema, nil)
			}
		}
	case "S":
		m.openPackageSchema()
	case "o":
		cmd := m.openSelectedSource()
		return m, cmd
	case "p":
		if m.focusPane == FocusInspector && m.detailCursor >= 0 && m.detailCursor < len(m.detailItems) {
			if detail := m.detailItems[m.detailCursor].packageMapping; detail != nil && detail.row.packagePath != "" {
				m.setStatus("Copied " + detail.row.packagePath)
				return m, osc52Copy(detail.row.packagePath)
			}
		}
		m.setStatus("Select a package mapping to copy its input path")
	case "y":
		if m.mapping != nil && m.mapping.cursor < len(m.mapping.rows) && m.focusPane == FocusMapping {
			path := m.mapping.rows[m.mapping.cursor].packagePath
			if path == "" {
				m.setStatus("No package path outside this mapping")
				return m, nil
			}
			m.setStatus("Copied " + path)
			return m, osc52Copy(path)
		}
		cmd := m.copySelectedPath()
		return m, cmd
	}
	return m, nil
}

func (m *Model) syncComponentCursor() {
	for index, name := range m.chartNames {
		if name == m.currentChart {
			m.componentCursor = index
			return
		}
	}
}

func osc52Copy(value string) tea.Cmd {
	payload := base64.StdEncoding.EncodeToString([]byte(value))
	return func() tea.Msg {
		// tea.Printf suppresses output in the TUI's alternate screen.
		fmt.Printf("\x1b]52;c;%s\a", payload)
		return nil
	}
}
