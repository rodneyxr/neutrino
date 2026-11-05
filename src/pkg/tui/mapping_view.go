package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

type mappingScreen struct {
	table              *mappingTable
	usage              introspect.PackageValueUsage
	pkg                *introspect.PackageValues
	bundle             *introspect.BundleMetadata
	origin             FocusPane
	rows               []mappingRow
	inside             int
	cursor             int
	offset             int
	showOthers         bool
	sourcePresent      bool
	chartKey           string
	previous           *mappingScreen
	returnRowDetail    bool
	returnDetailCursor int
	returnDetailOffset int
	returnDetailRef    *DisplayItem
	returnPanel        detailPanel
	returnPanels       []detailPanelReturn
	returnSource       *DetailItem
	returnTraceHistory []traceReturn
}

func (m *Model) openMapping(ref *valueMapping, origin FocusPane) {
	table, err := m.valuePolicy.open(ref)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	screen := &mappingScreen{
		table: table, usage: ref.usage, pkg: ref.pkg,
		chartKey: ref.chartKey, sourcePresent: table.sourcePresent, inside: table.inside,
		bundle: m.bundle, origin: origin,
		previous: m.mapping, returnRowDetail: m.mappingRowDetail,
		returnDetailCursor: m.detailCursor, returnDetailOffset: m.detailScrollOffset,
		returnDetailRef: m.detailRef,
		returnPanel:     m.detailPanel, returnPanels: append([]detailPanelReturn(nil), m.detailPanelHistory...), returnSource: m.detailSource,
		returnTraceHistory: append([]traceReturn(nil), m.traceHistory...),
	}
	m.mapping = screen
	m.mappingRowDetail = false
	m.detailRef = nil
	m.resetDetailPanels()
	m.mapping.selectRows()
	m.focusPane = FocusMapping
	m.syncInspectorSelection()
}

func (m *Model) focusMappingPath(path string) {
	if m.mapping == nil {
		return
	}
	for i, row := range m.mapping.rows {
		if packagePath(row.path) == packagePath(path) {
			m.mapping.cursor = i
			m.ensureMappingCursorVisible()
			m.syncInspectorSelection()
			return
		}
	}
}

func (screen *mappingScreen) selectRows() {
	selectedPath := ""
	if screen.cursor < len(screen.rows) {
		selectedPath = screen.rows[screen.cursor].path
	}
	screen.rows = screen.table.rows
	if !screen.showOthers {
		screen.rows = screen.rows[:screen.table.inside]
	}
	if len(screen.rows) == 0 {
		screen.cursor, screen.offset = 0, 0
		return
	}
	if selectedPath != "" {
		for i := range screen.rows {
			if screen.rows[i].path == selectedPath {
				screen.cursor = i
				break
			}
		}
	}
	screen.cursor = clamp(screen.cursor, 0, len(screen.rows)-1)
	screen.offset = min(screen.offset, screen.cursor)
}

func (m *Model) moveMappingCursor(delta int) {
	if m.mapping == nil || len(m.mapping.rows) == 0 {
		return
	}
	m.mapping.cursor = clamp(m.mapping.cursor+delta, 0, len(m.mapping.rows)-1)
	m.ensureMappingCursorVisible()
	m.syncInspectorSelection()
}

func (m *Model) moveMappingBoundary(last bool) {
	if m.mapping == nil || len(m.mapping.rows) == 0 {
		return
	}
	m.mapping.cursor = 0
	if last {
		m.mapping.cursor = len(m.mapping.rows) - 1
	}
	m.ensureMappingCursorVisible()
	m.syncInspectorSelection()
}

func (m *Model) ensureMappingCursorVisible() {
	if m.mapping == nil {
		return
	}
	visible := max(1, m.windowHeight-8)
	if m.mapping.cursor < m.mapping.offset {
		m.mapping.offset = m.mapping.cursor
	}
	if m.mapping.cursor >= m.mapping.offset+visible {
		m.mapping.offset = m.mapping.cursor - visible + 1
	}
}

func mappingRowStyle(status string) lipgloss.Style {
	switch status {
	case "available":
		return mappedValueStyle
	case "excluded":
		return excludedValueStyle
	case "unresolved", "runtime":
		return zarfVarStyle
	default:
		return helpStyle
	}
}

func mappingCell(value interface{}, present bool) string {
	if !present {
		return "—"
	}
	if value == nil {
		return "null"
	}
	return formatDefaultValue(value)
}

func (screen *mappingScreen) packageLabel() string {
	if screen.bundle != nil && screen.bundle.Format == "next" {
		return "Bundle package ID"
	}
	return "Package"
}

type mappingRowCells struct {
	path, value, input, status                     string
	pathWidth, valueWidth, inputWidth, statusWidth int
}

func mappingColumnWidths(inner int) (path, status, value, input int) {
	if inner < 32 {
		path = max(1, (inner-3)*55/100)
		return path, 0, max(1, inner-path-3), 0
	}
	path, status = max(8, inner*45/100), 10
	value = max(1, inner-path-status-4)
	if inner >= 100 {
		value = (inner - path - status - 5) / 2
		input = inner - path - status - value - 5
	}
	return
}

func (m Model) mappingRowCells(row mappingRow, inner int) mappingRowCells {
	pathWidth, statusWidth, valueWidth, inputWidth := mappingColumnWidths(inner)
	path := row.path
	if row.undiscovered {
		path += " [not discovered]"
	}
	chartValue := DisplayItem{ValueUsage: row.value, Component: m.mapping.chartKey}
	value := mappingCell(m.displayValue(chartValue, row.defaultValue), !row.undiscovered && (row.value.DefaultPresent || row.defaultValue != nil))
	input := "not supplied"
	if row.status == "other" {
		input = "not applicable"
	} else if row.present {
		var schema map[string]interface{}
		if m.mapping.pkg != nil {
			schema = schemaForPath(m.mapping.pkg.Schema, row.packagePath)
		}
		input = mappingCell(traceSafeValueWithSchema(row.packagePath, row.input, schema), true)
	}
	return mappingRowCells{path: terminalText(path), value: terminalText(value), input: terminalText(input), status: terminalText(row.status),
		pathWidth: pathWidth, valueWidth: valueWidth, inputWidth: inputWidth, statusWidth: statusWidth}
}

func (m Model) mappingPane(width, height int) string {
	screen := m.mapping
	inner := max(1, width-4)
	var content strings.Builder
	add := func(line string) { content.WriteString(truncateCell(line, inner) + "\n") }
	add(paneHeadingStyle.Render("Chart paths · mapping rule"))
	add(screen.usage.ComponentName + "/" + screen.usage.ChartName)
	add("Target: " + screen.usage.TargetPath + " · a other paths")
	pathWidth, statusWidth, defaultWidth, inputWidth := mappingColumnWidths(inner)
	heading := "  " + padRowCell(scrollRowCell("CHART PATH", 0, pathWidth), pathWidth)
	if statusWidth > 0 {
		heading += " " + padRowCell(scrollRowCell("STATUS", 0, statusWidth), statusWidth)
	}
	heading += " " + padRowCell(scrollRowCell("CHART INPUT", 0, defaultWidth), defaultWidth)
	if inputWidth > 0 {
		heading += " SETTING INPUT"
	}
	add(tableHeaderStyle.Render(heading))
	available := max(1, height-6)
	for index := screen.offset; index < len(screen.rows) && index < screen.offset+available; index++ {
		row := screen.rows[index]
		cells := m.mappingRowCells(row, inner)
		pathOffset, valueOffset := 0, 0
		if index == screen.cursor && m.focusPane == FocusMapping {
			pathOffset, valueOffset = m.pathScroll, m.valueScroll
		}
		line := padRowCell(scrollRowCell(cells.path, pathOffset, cells.pathWidth), cells.pathWidth)
		if cells.statusWidth > 0 {
			line += " " + padRowCell(scrollRowCell(cells.status, 0, cells.statusWidth), cells.statusWidth)
		}
		line += " " + padRowCell(scrollRowCell(cells.value, valueOffset, cells.valueWidth), cells.valueWidth)
		if inputWidth > 0 {
			line += " " + padRowCell(scrollRowCell(cells.input, valueOffset, cells.inputWidth), cells.inputWidth)
		}
		prefix := "  "
		if index == screen.cursor {
			prefix = "> "
		}
		line = truncateCell(prefix+line, inner)
		if index == screen.cursor {
			content.WriteString(selectedRowStyle.Render(line) + "\n")
		} else {
			content.WriteString(mappingRowStyle(row.status).Render(line) + "\n")
		}
	}
	if len(screen.rows) == 0 {
		add(helpStyle.Render("No chart values discovered in this destination."))
	}
	return paneFrame(width, height, m.focusPane == FocusMapping).Render(strings.TrimSuffix(content.String(), "\n"))
}
