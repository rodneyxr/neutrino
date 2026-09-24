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
	commandOpenSource
	commandCopyPath
	commandHelp
	commandQuit
)

type commandDefinition struct {
	id          commandID
	name        string
	key         string
	description string
}

var commands = [...]commandDefinition{
	{commandSearch, "Filter values", "/", "Search paths across the selected component"},
	{commandComponents, "Select component", "f", "Filter or switch the component scope"},
	{commandNextComponent, "Next component", "n / ]", "Jump to the next component"},
	{commandPrevComponent, "Previous component", "N / [", "Jump to the previous component"},
	{commandZarfSummary, "Toggle package variables", "Z", "Show the package variable summary"},
	{commandUDSSummary, "Toggle bundle overrides", "U", "Show the cross-package override summary"},
	{commandBundleMode, "Toggle bundle authoring", "b", "Show UDS authoring snippets in Details"},
	{commandOpenSource, "Open source location", "o", "Open the selected value in $EDITOR"},
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
	if m.focusPane == FocusInspector {
		m.focusPane = FocusValues
		m.pathScroll = 0
		m.pathScrollPause = 2
		m.compScroll = 0
		m.compScrollPause = 0
		return
	}
	if m.focusPane == FocusValues {
		m.focusPane = FocusComponents
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
		m.detailCursor = 0
		m.detailScrollOffset = 0
		m.buildDetailItems()
		m.focusPane = FocusInspector
	case FocusInspector:
		if m.detailCursor < 0 || m.detailCursor >= len(m.detailItems) {
			return nil
		}
		detail := m.detailItems[m.detailCursor]
		if detail.Kind == DetailItemLocation {
			return openEditor(detail.Location.File, detail.Location.Line, detail.Location.Column)
		}
		if detail.RefIndex >= 0 && detail.RefIndex < len(m.filteredValues) {
			m.cursor = detail.RefIndex
			m.detailCursor = 0
			m.buildDetailItems()
			m.ensureCursorVisible()
		}
	}
	return nil
}

func (m *Model) beginFilter() {
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
	case commandSearch:
		m.beginFilter()
	case commandComponents:
		m.beginComponentFilter()
	case commandNextComponent:
		m.jumpComponent(1)
	case commandPrevComponent:
		m.jumpComponent(-1)
	case commandZarfSummary:
		m.toggleZarfSummary()
	case commandUDSSummary:
		m.toggleUDSSummary()
	case commandBundleMode:
		m.detailBundleMode = !m.detailBundleMode
		m.detailScrollOffset = 0
		m.setStatus(toggleStatus("Bundle authoring", m.detailBundleMode))
	case commandOpenSource:
		return m.openSelectedSource()
	case commandCopyPath:
		return m.copySelectedPath()
	case commandHelp:
		m.showHelp = true
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
	value, ok := m.selectedValue()
	if !ok {
		m.setStatus("No source location for the selected value")
		return nil
	}
	if len(value.Locations) > 0 {
		location := value.Locations[0]
		m.setStatus("Opening " + filepath.Base(location.File))
		return openEditor(location.File, location.Line, location.Column)
	}
	for _, detail := range m.detailItems {
		if detail.Kind == DetailItemLocation {
			m.setStatus("Opening " + filepath.Base(detail.Location.File))
			return openEditor(detail.Location.File, detail.Location.Line, detail.Location.Column)
		}
	}
	m.setStatus("No source location for the selected value")
	return nil
}

func (m *Model) copySelectedPath() tea.Cmd {
	value, ok := m.selectedValue()
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
	if m.focusPane == FocusInspector {
		body = m.inspectorPane(width, bodyHeight)
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

	return header + "\n" + body + "\n" + footer
}

func (m Model) workspaceHeader(width int) string {
	chart := m.currentChart
	if chart == "" {
		chart = m.componentLabel(chart)
	}
	left := titleStyle.Render("◇ NEUTRINO") + "  " + headerStyle.Render(chart)
	right := fmt.Sprintf("%d values  •  %s", len(m.filteredValues), layoutLabel(m.layout()))
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
		primary = m.filterInput.View() + helpStyle.Render("  enter apply  esc clear")
	case InputModeChartSelect:
		primary = m.chartInput.View() + helpStyle.Render("  ↑↓ choose  enter apply  esc cancel")
	default:
		if m.focusPane == FocusInspector {
			primary = helpStyle.Render("j/k scroll & select  enter open  y copy path  b authoring  esc back  ? help")
		} else {
			primary = helpStyle.Render("tab/arrows panes  j/k move  J/K 5-lines  n/N comp  enter details  / search  : cmd  ? help  q quit")
		}
	}
	if m.inputMode == InputModeNormal && m.statusMessage != "" {
		hint := helpStyle.Render(": commands  ? help")
		gap := width - lipgloss.Width(m.statusMessage) - lipgloss.Width(hint) - 2
		if gap < 1 {
			return footerBarStyle.Width(width).Render(truncateCell(m.statusMessage, width-2))
		}
		return footerBarStyle.Width(width).Render(m.statusMessage + strings.Repeat(" ", gap) + hint)
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
		pathWidth = clamp(innerWidth*45/100, 28, 38)
		valWidth = max(12, innerWidth-pathWidth-3)
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
	runes := []rune(value.Path)
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
		if index == start || value.Component != lastComponent {
			if linesUsed+2 > maxLines {
				break
			}
			content.WriteString(componentBandStyle.Render(componentBand(m.valueComponentLabel(value, innerWidth), innerWidth)))
			content.WriteByte('\n')
			linesUsed++
			lastComponent = value.Component
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
		path := truncateCell(value.Path, pathWidth)
		if index == m.cursor && m.pathScroll > 0 {
			path = scrollCell(value.Path, m.pathScroll, pathWidth)
		}
		defaultValue := truncateCell(displayValue(value), valWidth)
		kind := displayKind(value)
		var line string
		if showMetadata {
			source := ""
			if len(value.Locations) > 1 {
				source = fmt.Sprintf("%d locations", len(value.Locations))
			} else if len(value.Locations) == 1 {
				source = fmt.Sprintf("%s:%d", filepath.Base(value.Locations[0].File), value.Locations[0].Line)
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
		content.WriteString(helpStyle.Render("No values match the current filters."))
	}
	return paneFrame(width, height, focused).Render(strings.TrimSuffix(content.String(), "\n"))
}

func (m Model) valueComponentLabel(value DisplayItem, width int) string {
	if value.IsPkgVar {
		return "Zarf package"
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

func valueSection(value DisplayItem) string {
	switch {
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
		if index == start || value.Component != lastComponent {
			lines++
			lastComponent = value.Component
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
	if value.IsUDSOverrideValue {
		return formatDefaultValue(value.OverrideValue)
	}
	return formatDefaultValue(value.DefaultValue)
}

func displayKind(value DisplayItem) string {
	switch {
	case value.IsPkgVar || value.IsZarfVar:
		return "variable"
	case value.IsUDSOverrideValue || value.IsUDSOverrideVar:
		return "override"
	case value.IsEmptyState:
		return "empty"
	case len(value.Locations) > 0:
		return "field"
	default:
		return "inferred"
	}
}

func kindStyleFor(value DisplayItem) lipgloss.Style {
	switch {
	case value.IsPkgVar || value.IsZarfVar:
		return zarfVarStyle
	case value.IsUDSOverrideValue:
		return udsOverrideValueStyle
	case value.IsUDSOverrideVar:
		return udsOverrideVarStyle
	case value.IsEmptyState:
		return helpStyle
	default:
		return codeStyle
	}
}

func (m Model) inspectorPane(width, height int) string {
	focused := m.focusPane == FocusInspector
	innerWidth := max(1, width-4)
	if _, ok := m.selectedValue(); !ok {
		return paneFrame(width, height, focused).Render(paneHeadingStyle.Render("DETAILS") + "\n" + helpStyle.Render("Select a value to inspect it."))
	}
	content, _ := m.inspectorContent(innerWidth, focused)
	return paneFrame(width, height, focused).Render(clipLinesAt(content, max(1, height-2), m.detailScrollOffset))
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
	return max(0, len(strings.Split(content, "\n"))-max(1, bodyHeight-2))
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
	visible := max(1, height-lipgloss.Height(m.workspaceHeader(width))-lipgloss.Height(m.workspaceFooter(width))-2)
	if row < m.detailScrollOffset {
		m.detailScrollOffset = row
	} else if row >= m.detailScrollOffset+visible {
		m.detailScrollOffset = row - visible + 1
	}
	m.detailScrollOffset = clamp(m.detailScrollOffset, 0, m.detailScrollLimit())
}

// inspectorContent returns the scrollable body and the selected action's line.
func (m Model) inspectorContent(innerWidth int, focused bool) (string, int) {
	value, ok := m.selectedValue()
	if !ok {
		return "", -1
	}

	var content strings.Builder
	lineNumber, selectedLine := 0, -1
	add := func(text string) {
		content.WriteString(text)
		content.WriteByte('\n')
		lineNumber += strings.Count(text, "\n") + 1
	}
	section := func(label string) {
		add("")
		rule := "━━ " + label + " "
		if remaining := innerWidth - lipgloss.Width(rule); remaining > 0 {
			rule += strings.Repeat("━", remaining)
		}
		add(fieldLabelStyle.Render(truncateCell(rule, innerWidth)))
	}

	add(paneHeadingStyle.Render("DETAILS"))
	key := value.Component
	if value.IsSummaryItem {
		key = value.SummaryComponent
	}
	_, component, chart := parseComponentKey(key)
	if key == "" || value.IsPkgVar {
		component, chart = "", ""
	}
	if result := m.components[key]; result != nil && result.ChartName != "" {
		chart = result.ChartName
	}
	if value.Chart != "" {
		chart = value.Chart
	}
	identity := func(label, text string) {
		if text == "" {
			text = "—"
		}
		add(fieldLabelStyle.Render(fmt.Sprintf("%-11s", label)) + " " + headerStyle.Render(wrapCell(text, max(1, innerWidth-12))))
	}
	identity("Component", component)
	identity("Chart", chart)
	identity("Value path", value.Path)
	valueType := "Helm chart value"
	switch {
	case value.IsPkgVar:
		valueType = "Zarf package variable"
	case value.IsZarfVar:
		valueType = "Zarf variable"
	case value.IsUDSOverrideValue:
		valueType = "UDS override value · fixed"
	case value.IsUDSOverrideVar:
		valueType = "UDS variable · configurable"
	}
	identity("Value type", valueType)
	if value.IsUDSOverrideVar && value.UDSVarPath != "" {
		identity("Controls", value.UDSVarPath)
	}

	if value.IsUDSOverrideValue {
		section("CHART DEFAULT")
		add(codeStyle.Render(wrapCell(formatYAMLValue(value.OriginalDefault), innerWidth)))
		section("EFFECTIVE VALUE")
		add(snippetStyle.Render(wrapCell(formatYAMLValue(value.OverrideValue), innerWidth)))
	} else {
		section("DEFAULT VALUE")
		add(codeStyle.Render(wrapCell(formatYAMLValue(value.DefaultValue), innerWidth)))
	}
	description := value.PkgVarDesc
	if value.UDSVarDescription != "" {
		description = value.UDSVarDescription
	}
	if description != "" {
		section("DESCRIPTION")
		add(wrapCell(description, innerWidth))
	}

	locations, refs := 0, 0
	for _, item := range m.detailItems {
		if item.Kind == DetailItemLocation {
			locations++
		} else {
			refs++
		}
	}
	section(fmt.Sprintf("TEMPLATE LOCATIONS  ·  %d", locations))
	if locations == 0 {
		add(helpStyle.Render("No template locations found."))
	}
	for index, detail := range m.detailItems {
		if detail.Kind != DetailItemLocation {
			continue
		}
		file := m.chartRelativeLocation(value, detail.Location.File)
		label := fmt.Sprintf("%s:%d:%d", file, detail.Location.Line, detail.Location.Column)
		if focused && index == m.detailCursor {
			selectedLine = lineNumber
			add(selectedRowStyle.Render("> " + tailCell(label, max(1, innerWidth-2))))
		} else {
			add("  " + tailCell(label, max(1, innerWidth-2)))
		}
	}
	if refs > 0 {
		section(fmt.Sprintf("RELATED VALUES  ·  %d", refs))
		for index, detail := range m.detailItems {
			if detail.Kind != DetailItemCrossRef {
				continue
			}
			label := truncateCell(detail.Label, max(1, innerWidth-2))
			if focused && index == m.detailCursor {
				selectedLine = lineNumber
				add(selectedRowStyle.Render("> " + label))
			} else {
				add("  " + label)
			}
		}
	}
	if m.detailBundleMode {
		if snippet := m.generateBundleSnippetForItem(value); snippet != "" {
			section("UDS AUTHORING")
			add(snippetStyle.Render(wrapCell(snippet, innerWidth)))
		}
	}
	return strings.TrimSuffix(content.String(), "\n"), selectedLine
}

// chartRelativeLocation changes only the display path; DetailItem keeps the
// original filename so Enter still opens the actual source file in the editor.
func (m Model) chartRelativeLocation(value DisplayItem, file string) string {
	key := value.Component
	if value.IsSummaryItem {
		key = value.SummaryComponent
	}
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
	paletteWidth := min(100, max(30, width-8))
	query := m.commandInput.Value()
	matchCount := commandMatchCount(query)
	var content strings.Builder
	content.WriteString(titleStyle.Render("COMMAND PALETTE") + "\n")
	content.WriteString(m.commandInput.View() + "\n\n")
	content.WriteString(tableHeaderStyle.Render(truncateCell(fmt.Sprintf("%-8s %-28s %s", "KEY", "ACTION", "DESCRIPTION"), paletteWidth-4)) + "\n")
	matchIndex := 0
	for _, command := range commands {
		if !commandMatches(command, query) {
			continue
		}
		line := fmt.Sprintf("%-8s %-28s %s", command.key, command.name, command.description)
		line = truncateCell(line, paletteWidth-4)
		if matchIndex == m.commandCursor {
			content.WriteString(selectedRowStyle.Render("> " + truncateCell(line, paletteWidth-6)))
		} else {
			content.WriteString("  " + truncateCell(line, paletteWidth-6))
		}
		content.WriteByte('\n')
		matchIndex++
	}
	if matchCount == 0 {
		content.WriteString(helpStyle.Render("No matching commands."))
	}
	content.WriteString("\n" + helpStyle.Render("↑↓ move  enter run  esc close"))
	panel := modalStyle.Width(paletteWidth - 2).Render(content.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) helpView(width, height int) string {
	content := titleStyle.Render("KEYBOARD HELP") + "\n\n" +
		"Tab / Shift+Tab   switch Components / Values\n" +
		"h / l, ← / →      focus left / right pane\n" +
		"j / k, ↑ / ↓      move 1 line in focused pane\n" +
		"J / K             jump 5 lines in focused pane\n" +
		"n / N, ] / [     jump next / previous component\n" +
		"g / G             first / last item\n" +
		"Enter             open full-width details\n" +
		"/                 filter values\n" +
		"f                 select component\n" +
		":                 command palette\n" +
		"Space             select component / Details authoring\n" +
		"o                 open source in $EDITOR\n" +
		"y                 copy value path\n" +
		"Z / U             summary toggles\n" +
		"b                 bundle authoring\n" +
		"Esc               back / clear\n" +
		"q                 quit\n\n" +
		helpStyle.Render("Press ? or Esc to close")
	panelWidth := min(58, max(34, width-4))
	panel := modalStyle.Width(panelWidth - 2).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func truncateCell(value string, width int) string {
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
		m.filterInput.SetValue("")
		m.updateFilteredValues()
		m.cursor = 0
		m.viewportOffset = 0
		m.buildDetailItems()
		m.setStatus("Value filter cleared")
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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.moveFocus(1)
	case "shift+tab":
		m.moveFocus(-1)
	case "h", "left":
		if m.focusPane == FocusInspector {
			m.focusPane = FocusValues
			m.pathScroll = 0
			m.pathScrollPause = 2
		} else {
			m.focusPane = FocusComponents
			m.pathScroll = 0
			m.compScroll = 0
			m.compScrollPause = 2
		}
	case "l", "right":
		if m.focusPane == FocusInspector {
			m.focusPane = FocusValues
			m.pathScroll = 0
			m.pathScrollPause = 6
		} else {
			m.focusPane = FocusValues
			m.pathScroll = 0
			m.pathScrollPause = 2
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
		cmd := m.activateFocused()
		return m, cmd
	case "/":
		m.beginFilter()
	case "f":
		m.beginComponentFilter()
	case ":":
		m.beginCommandPalette()
	case "?":
		m.showHelp = true
	case "esc":
		if m.focusPane == FocusInspector {
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
			m.detailBundleMode = !m.detailBundleMode
			m.detailScrollOffset = 0
			m.setStatus(toggleStatus("Bundle authoring", m.detailBundleMode))
		}
	case "Z":
		m.toggleZarfSummary()
	case "U":
		m.toggleUDSSummary()
	case "b":
		m.detailBundleMode = !m.detailBundleMode
		m.detailScrollOffset = 0
		m.setStatus(toggleStatus("Bundle authoring", m.detailBundleMode))
	case "o":
		cmd := m.openSelectedSource()
		return m, cmd
	case "y":
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
	return tea.Printf("\x1b]52;c;%s\a", payload)
}
