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
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
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
	{commandSearch, "Filter values", "/", "Search paths across the selected component"},
	{commandComponents, "Select package or chart", "f", "Navigate the package, component and chart hierarchy"},
	{commandNextComponent, "Next component", "n / ]", "Jump to the next component"},
	{commandPrevComponent, "Previous component", "N / [", "Jump to the previous component"},
	{commandZarfSummary, "Toggle package variables", "Z", "Show the package variable summary"},
	{commandUDSSummary, "Toggle bundle overrides", "U", "Show the cross-package override summary"},
	{commandBundleMode, "Toggle authoring snippets", "b", "Show Package Values and UDS authoring snippets in Details"},
	{commandSchema, "View value schema", "s", "Open schema annotations in a separate view"},
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
	if m.focusPane == FocusMapping {
		return
	}
	if m.focusPane == FocusInspector {
		if m.mappingRowDetail {
			return
		}
		m.focusPane = FocusValues
		m.pathScroll = 0
		m.pathScrollPause = 2
		m.compScroll = 0
		m.compScrollPause = 0
		return
	}
	if m.focusPane == FocusValues {
		m.focusPane = FocusComponents
		if m.nextRail {
			return
		}
		m.pathScroll = 0
		m.pathScrollPause = 0
		m.compScroll = 0
		m.compScrollPause = 2
	} else {
		m.focusPane = FocusValues
		m.pathScroll = 0
		m.pathScrollPause = 2
		m.compScroll = 0
		m.compScrollPause = 0
	}
}

func (m *Model) moveWithinFocusedPane(delta int) {
	switch m.focusPane {
	case FocusComponents:
		if m.nextRail {
			m.selectNextCategory(clamp(m.category+delta, 0, len(nextCategories)-1))
			return
		}
		prevComp := m.componentCursor
		m.componentCursor = clamp(m.componentCursor+delta, 0, len(m.chartNames)-1)
		if m.componentCursor != prevComp {
			m.compScroll = 0
			m.compScrollPause = 2
		}
	case FocusValues:
		prevVal := m.cursor
		m.cursor = clamp(m.cursor+delta, 0, len(m.filteredValues)-1)
		if m.cursor != prevVal {
			m.pathScroll = 0
			m.pathScrollPause = 2
		}
		m.ensureCursorVisible()
		m.buildDetailItems()
	case FocusMapping:
		m.moveMappingCursor(delta)
	case FocusInspector:
		m.detailCursor = clamp(m.detailCursor+delta, 0, len(m.detailItems)-1)
		m.detailScrollOffset = clamp(m.detailScrollOffset+delta, 0, m.detailScrollLimit())
		m.ensureDetailCursorVisible()
	}
}

func (m *Model) moveFocusedToBoundary(last bool) {
	target := 0
	switch m.focusPane {
	case FocusComponents:
		if m.nextRail {
			if last {
				m.selectNextCategory(len(nextCategories) - 1)
			} else {
				m.selectNextCategory(0)
			}
			return
		}
		prevComp := m.componentCursor
		if last {
			target = len(m.chartNames) - 1
		}
		m.componentCursor = clamp(target, 0, len(m.chartNames)-1)
		if m.componentCursor != prevComp {
			m.compScroll = 0
			m.compScrollPause = 2
		}
	case FocusValues:
		prevVal := m.cursor
		if last {
			target = len(m.filteredValues) - 1
		}
		m.cursor = clamp(target, 0, len(m.filteredValues)-1)
		if m.cursor != prevVal {
			m.pathScroll = 0
			m.pathScrollPause = 2
		}
		m.ensureCursorVisible()
		m.buildDetailItems()
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
		if m.nextRail {
			m.selectNextCategory(clamp(m.category+delta, 0, len(nextCategories)-1))
			return
		}
		previous := m.componentCursor
		m.componentCursor = clamp(m.componentCursor+delta, 0, len(m.chartNames)-1)
		if m.componentCursor != previous {
			m.compScroll = 0
			m.compScrollPause = 2
		}
	case FocusValues:
		if len(m.filteredValues) == 0 {
			return
		}
		if delta > 0 {
			curr := m.filteredValues[m.cursor].Component
			for i := m.cursor + 1; i < len(m.filteredValues); i++ {
				if m.filteredValues[i].Component != curr {
					m.cursor = i
					m.pathScroll = 0
					m.pathScrollPause = 2
					m.ensureCursorVisible()
					m.buildDetailItems()
					return
				}
			}
		} else {
			curr := m.filteredValues[m.cursor].Component
			startIdx := m.cursor
			for startIdx > 0 && m.filteredValues[startIdx-1].Component == curr {
				startIdx--
			}
			if startIdx == m.cursor && startIdx > 0 {
				prevIdx := startIdx - 1
				prevComp := m.filteredValues[prevIdx].Component
				for prevIdx > 0 && m.filteredValues[prevIdx-1].Component == prevComp {
					prevIdx--
				}
				m.cursor = prevIdx
			} else {
				m.cursor = startIdx
			}
			m.pathScroll = 0
			m.pathScrollPause = 2
			m.ensureCursorVisible()
			m.buildDetailItems()
		}
	}
}

func (m *Model) activateFocused() tea.Cmd {
	switch m.focusPane {
	case FocusComponents:
		if m.nextRail {
			m.focusPane = FocusValues
			return nil
		}
		if len(m.chartNames) == 0 {
			return nil
		}
		m.currentChart = m.chartNames[m.componentCursor]
		m.updateFilteredValues()
		m.cursor = 0
		m.viewportOffset = 0
		m.focusPane = FocusValues
		m.pathScroll = 0
		m.pathScrollPause = 2
		m.buildDetailItems()
		m.setStatus("Showing " + m.componentLabel(m.currentChart))
	case FocusValues:
		if len(m.filteredValues) == 0 || m.filteredValues[m.cursor].IsEmptyState {
			return nil
		}
		m.detailRef = nil
		m.traceHistory = nil
		m.resetDetailPanels()
		value := m.filteredValues[m.cursor]
		if value.IsPackageMapping && value.ValueTarget != "" {
			m.openMapping(value.mappingRef, FocusValues)
			return nil
		}
		m.detailCursor = 0
		m.detailScrollOffset = 0
		m.detailSchemaShown = false
		m.statusMessage = ""
		m.buildDetailItems()
		m.focusPane = FocusInspector
		m.ensureDetailCursorVisible()
	case FocusMapping:
		if m.mapping != nil && m.mapping.cursor < len(m.mapping.rows) {
			m.resetDetailPanels()
			m.mappingRowDetail = true
			m.detailRef = nil
			m.detailCursor = 0
			m.detailScrollOffset = 0
			m.detailSchemaShown = false
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
			m.resetDetailPanels()
			m.detailRef = detail.relatedValue
			m.detailCursor, m.detailScrollOffset = 0, 0
			m.buildDetailItems()
		}
	}
	return nil
}

func (m *Model) beginFilter() {
	m.previousFilter = m.filterInput.Value()
	m.allCategories = true
	m.showOverview = false
	m.updateFilteredValues()
	m.inputMode = InputModeFilter
	m.filterInput.Focus()
	m.setStatus("Type to filter values")
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
		m.focusPane = FocusValues
	case commandAllInputs:
		m.showOverview = false
		m.allCategories = true
		m.updateFilteredValues()
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
	m.buildDetailItems()
	m.setStatus(toggleStatus("Package variable summary", m.showZarfSummary))
}

func (m *Model) toggleUDSSummary() {
	m.showUDSSummary = !m.showUDSSummary
	m.updateFilteredValues()
	m.cursor = 0
	m.viewportOffset = 0
	m.buildDetailItems()
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
	value, ok := m.detailValue()
	if !ok {
		m.setStatus("No source location for the selected value")
		return nil
	}
	if len(m.inspectorSources(value)) > 0 {
		if m.focusPane != FocusInspector {
			m.focusPane = FocusInspector
			m.resetDetailPanels()
			m.buildDetailItems()
		}
		m.openDetailPanel(detailSources, nil)
		return nil
	}
	m.setStatus("No source location for the selected value")
	return nil
}

func (m *Model) copySelectedPath() tea.Cmd {
	value, ok := m.detailValue()
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
	if m.focusPane == FocusMapping {
		body = m.mappingPane(width, bodyHeight)
	} else if m.focusPane == FocusInspector {
		body = m.inspectorPane(width, bodyHeight)
	} else if m.showOverview {
		body = m.overviewPane(width, bodyHeight)
	} else if m.nextRail {
		body = m.nextCategoryWorkspace(width, bodyHeight)
	} else if m.layout() == layoutCompact {
		if m.focusPane == FocusComponents {
			body = m.componentPane(width, bodyHeight)
		} else {
			body = m.valuesPane(width, bodyHeight)
		}
	} else {
		compWidth := m.componentWidth(width)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.componentPane(compWidth, bodyHeight),
			m.valuesPane(width-compWidth, bodyHeight),
		)
	}

	body = fitWorkspace(body, width, bodyHeight)
	return header + "\n" + body + "\n" + footer
}

func (m Model) workspaceHeader(width int) string {
	chart := terminalText(m.scopeLabel())
	left := titleStyle.Render("◇ NEUTRINO") + "  " + headerStyle.Render(chart)
	right := fmt.Sprintf("%d values  •  %s", len(m.filteredValues), layoutLabel(m.layout()))
	if m.nextRail {
		mode := nextCategories[m.category]
		if m.allCategories {
			mode = "all inputs"
		}
		if m.showOverview {
			mode = "overview"
		}
		right = fmt.Sprintf("%d values · %s", len(m.filteredValues), mode)
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
	return "2 panes"
}
func (m Model) workspaceFooter(width int) string {
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
		case m.focusPane == FocusMapping:
			primary = helpStyle.Render("j/k move  enter details  a other chart paths  y copy package path  esc back")
		case m.focusPane == FocusInspector:
			primary = helpStyle.Render(m.inspectorFooter(width))
		default:
			if m.nextRail {
				primary = helpStyle.Render("0 overview  1-7 category  8 all  f scope  / search  c changed  u unresolved  ? help  q quit")
			} else {
				primary = helpStyle.Render("tab/arrows panes  j/k move  J/K 5-lines  n/N comp  enter details  / search  : cmd  ? help  q quit")
			}
		}
	}
	if m.inputMode == InputModeNormal && m.statusMessage != "" {
		hint := helpStyle.Render(": commands  ? help")
		if m.focusPane == FocusInspector {
			hint = helpStyle.Render("? help  esc back")
		}
		gap := width - lipgloss.Width(m.statusMessage) - lipgloss.Width(hint) - 2
		if gap < 1 {
			return footerBarStyle.Width(width).Render(truncateCell(m.statusMessage, width-2))
		}
		return footerBarStyle.Width(width).Render(m.statusMessage + strings.Repeat(" ", gap) + hint)
	}
	if (width < 100 || m.nextRail && width < 115) && m.inputMode == InputModeNormal && m.statusMessage == "" {
		primary = helpStyle.Render("enter open  / search  f scope  ? help")
		if m.showOverview {
			primary = helpStyle.Render("j/k scroll  8 browse  ? help")
		}
		if m.focusPane == FocusMapping {
			primary = helpStyle.Render("j/k move  enter open  esc back  ? help")
		}
		if m.focusPane == FocusInspector {
			primary = helpStyle.Render(m.inspectorFooter(width))
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
	if innerWidth < 65 {
		pathWidth = max(8, min(38, innerWidth*55/100))
		valWidth = max(1, innerWidth-pathWidth-3)
		return pathWidth, valWidth, 0, 0, false
	}

	typeWidth = 10
	pathWidth = clamp(innerWidth*38/100, 30, 44)
	valWidth = clamp(innerWidth*28/100, 18, 30)
	srcWidth = max(12, innerWidth-pathWidth-valWidth-typeWidth-5)
	return pathWidth, valWidth, typeWidth, srcWidth, true
}

func (m Model) currentPathWidth() int {
	width := m.windowWidth
	if width < 1 {
		width = 100
	}
	valuesWidth := width
	if m.layout() != layoutCompact {
		valuesWidth = width - m.componentWidth(width)
	}
	innerWidth := max(1, valuesWidth-4)
	pathWidth, _, _, _, _ := m.columnWidths(innerWidth)
	return pathWidth
}

func (m *Model) stepAutoScroll() {
	if m.inputMode != InputModeNormal {
		return
	}
	if m.focusPane == FocusComponents {
		m.stepComponentScroll()
	} else if m.focusPane == FocusValues {
		m.stepValuePathScroll()
	}
}

func (m *Model) stepComponentScroll() {
	if m.componentCursor < 0 || m.componentCursor >= len(m.chartNames) {
		m.compScroll = 0
		m.compScrollPause = 0
		return
	}
	width := m.componentWidth(m.windowWidth)
	name := m.chartNames[m.componentCursor]
	displayName := m.componentDisplayName(name, width)
	prefixWidth := 2
	if name == m.currentChart {
		prefixWidth = 4
	}
	maxVisible := max(1, width-4-prefixWidth)
	runes := []rune(displayName)
	if len(runes) <= maxVisible {
		m.compScroll = 0
		m.compScrollPause = 0
		return
	}

	maxScroll := max(1, len(runes)-maxVisible+1)
	if m.compScrollPause > 0 {
		m.compScrollPause--
		return
	}

	if m.compScroll < maxScroll {
		m.compScroll++
		if m.compScroll == maxScroll {
			m.compScrollPause = 6
		}
	} else {
		m.compScroll = 0
		m.compScrollPause = 3
	}
}

func (m *Model) stepValuePathScroll() {
	value, ok := m.selectedValue()
	if !ok {
		m.pathScroll = 0
		m.pathScrollPause = 0
		return
	}
	runes := []rune(m.valueListPath(value))
	pathWidth := m.currentPathWidth()
	if len(runes) <= pathWidth {
		m.pathScroll = 0
		m.pathScrollPause = 0
		return
	}

	maxScroll := max(1, len(runes)-pathWidth+1)
	if m.pathScrollPause > 0 {
		m.pathScrollPause--
		return
	}

	if m.pathScroll < maxScroll {
		m.pathScroll++
		if m.pathScroll == maxScroll {
			m.pathScrollPause = 6
		}
	} else {
		m.pathScroll = 0
		m.pathScrollPause = 3
	}
}
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
	focused := m.focusPane == FocusValues
	innerWidth := max(1, width-4)
	contentHeight := max(1, height-4)
	var content strings.Builder
	content.WriteString(paneHeadingStyle.Render("VALUES"))
	if filter := m.filterInput.Value(); filter != "" {
		content.WriteString(filterStyle.Render("  /" + truncateCell(filter, max(1, innerWidth-12))))
	}
	content.WriteByte('\n')

	pathWidth, valWidth, typeWidth, srcWidth, showMetadata := m.columnWidths(innerWidth)
	if showMetadata {
		content.WriteString(tableHeaderStyle.Render(truncateCell(fmt.Sprintf("  %-*s %-*s %-*s %s", pathWidth, "VALUE PATH", valWidth, "VALUE", typeWidth, "TYPE", "SOURCE"), innerWidth)))
	} else {
		content.WriteString(tableHeaderStyle.Render(fmt.Sprintf("  %-*s %s", pathWidth, "VALUE PATH", "VALUE")))
	}
	content.WriteByte('\n')

	maxLines := contentHeight
	linesUsed := 0
	start := clamp(m.viewportOffset, 0, max(0, len(m.filteredValues)-1))
	lastComponent := ""
	lastSection := ""
	for index := start; index < len(m.filteredValues); index++ {
		value := m.filteredValues[index]
		group := valueGroupKey(value)
		if index == start || group != lastComponent {
			if linesUsed+2 > maxLines {
				break
			}
			content.WriteString(componentBandStyle.Render(componentBand(m.valueComponentLabel(value, innerWidth), innerWidth)))
			content.WriteByte('\n')
			linesUsed++
			lastComponent = group
			lastSection = ""
		}
		section := valueSection(value)
		if section != lastSection {
			if lastSection != "" {
				if linesUsed+3 > maxLines {
					break
				}
				content.WriteByte('\n')
				linesUsed++
			} else if linesUsed+2 > maxLines {
				break
			}
			content.WriteString(groupHeadingStyle.Render(truncateCell("  "+section, innerWidth)))
			content.WriteByte('\n')
			linesUsed++
			lastSection = section
		}
		if linesUsed+1 > maxLines {
			break
		}

		marker := "  "
		if index == m.cursor {
			marker = "> "
		}
		displayPath := m.valueListPath(value)
		path := truncateCell(displayPath, pathWidth)
		if index == m.cursor && m.pathScroll > 0 {
			path = scrollCell(displayPath, m.pathScroll, pathWidth)
		}
		safeValue := value
		safeValue.DefaultValue = m.displayValue(value, value.DefaultValue)
		safeValue.OverrideValue = m.displayValue(value, value.OverrideValue)
		defaultValue := displayValue(safeValue)
		if !showMetadata && value.IsPackageValue && value.MappingStatus != "" {
			defaultValue = value.MappingStatus + ": " + defaultValue
		}
		defaultValue = truncateCell(defaultValue, valWidth)
		kind := displayKind(value)
		if m.valueUnresolved(value) && !runtimeReference(value) && !value.Wildcard {
			kind = "unresolved"
		} else if m.valueChanged(value) {
			kind = "changed"
		}
		var line string
		if showMetadata {
			source := ""
			count := len(value.Locations)
			first := introspect.ValueUsageLocation{}
			if count > 0 {
				first = value.Locations[0]
			}
			if len(value.ZarfReferences) > 0 {
				count, first = value.ZarfLocationCount, value.ZarfFirstLocation
			}
			if count > 1 {
				source = fmt.Sprintf("%d locations", count)
			} else if count == 1 {
				source = filepath.Base(first.File)
				if runtimeReference(value) {
					source = "template use"
				}
				if first.Line > 0 {
					source += fmt.Sprintf(":%d", first.Line)
				}
			}
			source = truncateCell(source, srcWidth)
			line = fmt.Sprintf("%s%-*s %-*s %-*s %s", marker, pathWidth, path, valWidth, defaultValue, typeWidth, kind, source)
		} else {
			line = fmt.Sprintf("%s%-*s %s", marker, pathWidth, path, defaultValue)
		}
		line = truncateCell(line, innerWidth)
		if index == m.cursor {
			content.WriteString(selectedRowStyle.Render(line))
		} else {
			content.WriteString(kindStyleFor(value).Render(line))
		}
		content.WriteByte('\n')
		linesUsed++
	}
	if len(m.filteredValues) == 0 {
		message := "No values match the current filters."
		if m.nextRail && m.filterInput.Value() == "" {
			message = "No items in this category for this component."
		}
		content.WriteString(helpStyle.Render(truncateCell(message, innerWidth)))
	}
	return paneFrame(width, height, focused).Render(strings.TrimSuffix(content.String(), "\n"))
}

// The package band carries the owner; keep the full path on DisplayItem for
// searching, Details, and copying.
func (m Model) valueListPath(value DisplayItem) string {
	if m.nextRail && (value.IsPkgVar || value.IsZarfVar) && value.PackageName != "" {
		return strings.TrimPrefix(value.Path, value.PackageName+".")
	}
	return value.Path
}

func (m Model) valueComponentLabel(value DisplayItem, width int) string {
	if m.nextRail && (value.IsPkgVar || value.IsZarfVar) && value.PackageName != "" {
		return "Zarf package · " + value.PackageName
	}
	if value.IsPkgVar {
		return "Zarf package"
	}
	if value.IsPackageValue || value.IsPackageMapping {
		if value.Component == "UDS Next bundle" {
			return "UDS Next bundle · " + value.PackageName
		}
		if value.PackageName != "" {
			return "Zarf Package Values · " + value.PackageName
		}
	}
	if value.IsSummaryItem {
		return "UDS bundle summary"
	}
	return m.componentDisplayName(value.Component, width)
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

func valueGroupKey(value DisplayItem) string {
	if (value.IsPkgVar || value.IsZarfVar) && value.PackageName != "" {
		return "Zarf package/" + value.PackageName
	}
	if value.IsPackageValue || value.IsPackageMapping {
		return value.Component + "/" + value.PackageName
	}
	return value.Component
}

func valueSection(value DisplayItem) string {
	switch {
	case value.IsBundleVariable:
		return "UDS Next variables · configured input"
	case value.IsPackageMapping:
		if value.Runtime {
			return "Zarf Package Values · runtime references"
		}
		return "Chart mappings · explore configurable values"
	case value.IsPackageValue:
		if value.Component == "UDS Next bundle" {
			return "UDS Next values files · operator input"
		}
		return "Zarf Package Values · configured paths"
	case value.IsPkgVar || value.IsZarfVar:
		return "Zarf variables"
	case value.IsUDSOverrideVar:
		return "UDS variables · configurable"
	case value.IsUDSOverrideValue:
		return "UDS overrides · fixed by bundle"
	default:
		return "Helm chart values"
	}
}

func (m Model) valueLineCount(start, end int) int {
	if start < 0 || end < start || start >= len(m.filteredValues) {
		return 0
	}
	end = min(end, len(m.filteredValues)-1)
	lines := 0
	lastComponent := ""
	lastSection := ""
	for index := start; index <= end; index++ {
		value := m.filteredValues[index]
		group := valueGroupKey(value)
		if index == start || group != lastComponent {
			lines++
			lastComponent = group
			lastSection = ""
		}
		section := valueSection(value)
		if section != lastSection {
			if lastSection != "" {
				lines++
			}
			lines++
			lastSection = section
		}
		lines++
	}
	return lines
}

func displayValue(value DisplayItem) string {
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
		return fmt.Sprintf("%d values files", len(value.ValueFiles))
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
		if value.MappingStatus != "" {
			return value.MappingStatus
		}
		return "package"
	case value.IsPkgVar || value.IsZarfVar:
		return "variable"
	case value.IsUDSOverrideValue || value.IsUDSOverrideVar:
		return "override"
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
	innerWidth := max(1, width-4)
	if _, ok := m.detailValue(); !ok {
		return paneFrame(width, height, focused).Render(paneHeadingStyle.Render("DETAILS") + "\n" + helpStyle.Render("Select a value to inspect it."))
	}
	content, _ := m.inspectorContent(innerWidth, focused)
	visible := max(1, height-3)
	total := strings.Count(content, "\n") + 1
	offset := clamp(m.detailScrollOffset, 0, max(0, total-visible))
	position := fmt.Sprintf("%d–%d / %d lines", offset+1, min(total, offset+visible), total)
	context := "Details"
	if offset > 0 {
		if value, ok := m.detailValue(); ok {
			context = value.Path
		}
	}
	gap := max(1, innerWidth-lipgloss.Width(position)-2)
	progress := fmt.Sprintf("%-*s  %s", gap, truncateCell(context, gap), position)
	frame := paneFrame(width, height, focused).Border(lipgloss.RoundedBorder())
	if m.detailPanel == detailSummary && total <= height-2 {
		return frame.Render(content + strings.Repeat("\n", max(0, height-2-lipgloss.Height(content))))
	}
	body := clipLinesAt(content, visible, offset)
	body += strings.Repeat("\n", max(0, visible-lipgloss.Height(body)))
	return frame.Render(body + "\n" + helpStyle.Render(truncateCell(progress, innerWidth)))
}

func (m Model) detailScrollLimit() int {
	width := m.windowWidth
	height := m.windowHeight
	if width < 1 {
		width = 100
	}
	if height < 1 {
		height = 30
	}
	bodyHeight := height - lipgloss.Height(m.workspaceHeader(width)) - lipgloss.Height(m.workspaceFooter(width))
	bodyHeight = max(4, bodyHeight)
	content, _ := m.inspectorContent(max(1, width-4), true)
	return max(0, len(strings.Split(content, "\n"))-max(1, bodyHeight-3))
}

func (m *Model) ensureDetailCursorVisible() {
	if len(m.detailItems) == 0 {
		return
	}
	width, height := m.windowWidth, m.windowHeight
	if width < 1 {
		width = 100
	}
	if height < 1 {
		height = 30
	}
	_, row := m.inspectorContent(max(1, width-4), true)
	visible := max(1, height-lipgloss.Height(m.workspaceHeader(width))-lipgloss.Height(m.workspaceFooter(width))-3)
	if row < m.detailScrollOffset {
		m.detailScrollOffset = row
	} else if row >= m.detailScrollOffset+visible {
		m.detailScrollOffset = row - visible + 1
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
	navigation := "jump next / previous component"
	space := "select component / Details authoring"
	enter := "details / mapping / mapped value"
	if m.nextRail {
		content += "1–7               select category\nTab / Shift+Tab   switch Categories / Values\n"
		navigation = "next / previous group or category"
		space = "focus values / Details authoring"
		enter = "focus values / details / mapping"
	} else {
		content += "Tab / Shift+Tab   switch Components / Values\n"
	}
	content += "h / l, ← / →      focus left / right pane\n" +
		"j / k, ↑ / ↓      move 1 line in focused pane\n" +
		"J / K             jump 5 lines in focused pane\n" +
		"n / N, ] / [     " + navigation + "\n" +
		"g / G             first / last item\n" +
		"Enter             " + enter + "\n" +
		"/                 search all inputs in scope\n" +
		"f                 package / component / chart\n" +
		":                 command palette\n" +
		"Space             " + space + "\n" +
		"a                 toggle other paths in mapping\n" +
		"o                 view source locations\n" +
		"y                 copy value path\n" +
		"p                 copy selected mapping input path\n" +
		"v / t / s         full value / history / schema\n" +
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
		m.buildDetailItems()
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
	if m.nextRail && (m.focusPane == FocusComponents || m.focusPane == FocusValues) {
		key := msg.String()
		if len(key) == 1 && key[0] >= '1' && key[0] <= '7' {
			m.selectNextCategory(int(key[0] - '1'))
			m.focusPane = FocusValues
			return m, nil
		}
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
	if m.focusPane == FocusMapping || m.mappingRowDetail {
		switch msg.String() {
		case "q", "ctrl+c", "j", "k", "J", "K", "up", "down", "g", "G", "pgup", "pgdown", "enter", "esc", "?", "y":
		case "p", "o", "b", " ", "space":
			if m.focusPane != FocusInspector {
				return m, nil
			}
		case "a":
			if m.focusPane != FocusMapping {
				return m, nil
			}
		default:
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
	case "h", "left":
		if m.focusPane == FocusMapping || m.mappingRowDetail {
			break
		}
		if m.focusPane == FocusInspector {
			m.focusPane = FocusValues
		} else {
			m.focusPane = FocusComponents
		}
		m.pathScroll = 0
		m.pathScrollPause = 2
	case "l", "right":
		if m.focusPane == FocusMapping || m.mappingRowDetail {
			break
		}
		m.focusPane = FocusValues
		m.pathScroll = 0
		m.pathScrollPause = 2
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
			m.allCategories = true
			m.updateFilteredValues()
			return m, nil
		}
		cmd := m.activateFocused()
		return m, cmd
	case "0":
		m.showOverview = !m.showOverview
		m.overviewOffset = 0
		m.focusPane = FocusValues
	case "8":
		m.showOverview = false
		m.allCategories = true
		m.focusPane = FocusValues
		m.updateFilteredValues()
		m.cursor, m.viewportOffset = 0, 0
		m.buildDetailItems()
	case "c":
		m.focusStatus("changed")
	case "u":
		m.focusStatus("unresolved")
	case "a":
		if m.focusPane == FocusMapping && m.mapping != nil {
			m.mapping.showOthers = !m.mapping.showOthers
			m.mapping.selectRows()
			m.ensureMappingCursorVisible()
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
		} else if m.mappingRowDetail {
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
			m.buildDetailItems()
		} else if m.focusPane == FocusInspector {
			m.focusPane = FocusValues
			m.detailScrollOffset = 0
		} else if m.filterInput.Value() != "" {
			m.filterInput.SetValue("")
			m.updateFilteredValues()
			m.cursor = 0
			m.viewportOffset = 0
			m.buildDetailItems()
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
