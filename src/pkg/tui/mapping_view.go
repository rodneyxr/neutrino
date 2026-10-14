package tui

import (
	"fmt"
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

func (m Model) mappingPane(width, height int) string {
	screen := m.mapping
	inner := max(1, width-4)
	var content strings.Builder
	add := func(line string) { content.WriteString(truncateCell(line, inner) + "\n") }
	add(paneHeadingStyle.Render("Mapped settings"))
	add(screen.usage.ComponentName + "/" + screen.usage.ChartName)
	add("Target: " + screen.usage.TargetPath + " · a other paths")
	pathWidth := max(10, inner*45/100)
	statusWidth := 10
	defaultWidth := max(8, inner-pathWidth-statusWidth-5)
	inputWidth := 0
	if inner >= 100 {
		defaultWidth = (inner - pathWidth - statusWidth - 6) / 2
		inputWidth = inner - pathWidth - statusWidth - defaultWidth - 6
	}
	heading := fmt.Sprintf("  %-*s %-*s %-*s", pathWidth, "CHART PATH", statusWidth, "STATUS", defaultWidth, "CHART DEFAULT")
	if inputWidth > 0 {
		heading += " PACKAGE INPUT"
	}
	add(tableHeaderStyle.Render(heading))
	available := max(1, height-6)
	for index := screen.offset; index < len(screen.rows) && index < screen.offset+available; index++ {
		row := screen.rows[index]
		path := row.path
		if row.undiscovered {
			path += " [not discovered]"
		}
		chartValue := DisplayItem{ValueUsage: row.value, Component: screen.chartKey}
		line := fmt.Sprintf("%-*s %-*s %-*s", pathWidth, truncateCell(path, pathWidth), statusWidth, row.status,
			defaultWidth, truncateCell(mappingCell(m.displayValue(chartValue, row.defaultValue), !row.undiscovered && (row.value.DefaultPresent || row.defaultValue != nil)), defaultWidth))
		if inputWidth > 0 {
			input := "not supplied"
			if row.status == "other" {
				input = "not applicable"
			} else if row.present {
				input = mappingCell(traceSafeValueWithSchema(row.packagePath, row.input, schemaForPath(screen.pkg.Schema, row.packagePath)), true)
			}
			line += " " + truncateCell(input, inputWidth)
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
