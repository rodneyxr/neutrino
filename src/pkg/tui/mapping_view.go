package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

type mappingRow struct {
	path         string // destination chart path
	defaultValue interface{}
	packagePath  string
	input        interface{}
	present      bool
	status       string
	exclusion    string
	undiscovered bool
}
type mappingScreen struct {
	usage              introspect.PackageValueUsage
	pkg                *introspect.PackageValues
	chart              *introspect.IntrospectResult
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
}

func (m *Model) openMapping(item DisplayItem, origin FocusPane) {
	usage := introspect.PackageValueUsage{
		Path: item.ValueSource, ComponentName: item.MappingComponent,
		ChartName: item.MappingChart, TargetPath: item.ValueTarget,
		ExcludePaths: item.ExcludedPaths, Runtime: item.Runtime,
	}
	packageName := item.PackageName
	if packageName == "" && item.Component != "Zarf Package Values" {
		packageName, _, _ = parseComponentKey(item.Component)
	}
	var pkg *introspect.PackageValues
	for i := range m.packageValues {
		candidate := &m.packageValues[i]
		if packageName != "" && candidate.PackageName != packageName {
			continue
		}
		for _, declared := range candidate.Usages {
			if declared.Path == usage.Path && declared.ComponentName == usage.ComponentName &&
				declared.ChartName == usage.ChartName && declared.TargetPath == usage.TargetPath &&
				strings.Join(declared.ExcludePaths, "\x00") == strings.Join(usage.ExcludePaths, "\x00") {
				pkg = candidate
				break
			}
		}
		if pkg != nil {
			break
		}
	}
	if pkg == nil {
		m.setStatus("Package for this mapping is unavailable")
		return
	}
	key := usage.ComponentName + "/" + usage.ChartName
	if pkg.PackageName != "" {
		if _, ok := m.components[pkg.PackageName+"/"+key]; ok {
			key = pkg.PackageName + "/" + key
		}
	}
	chart := m.components[key]
	if chart == nil {
		m.setStatus("Destination chart for this mapping is unavailable")
		return
	}
	screen := &mappingScreen{usage: usage, pkg: pkg, chart: chart, chartKey: key, bundle: m.bundle, origin: origin,
		previous: m.mapping, returnRowDetail: m.mappingRowDetail,
		returnDetailCursor: m.detailCursor, returnDetailOffset: m.detailScrollOffset,
		returnDetailRef: m.detailRef}
	m.mapping = screen
	m.mappingRowDetail = false
	m.detailRef = nil
	m.mapping.buildRows()
	m.focusPane = FocusMapping
}

// mappingInput looks up a supplied path without conflating absent, null, false,
// zero, and empty objects. A scalar ancestor cannot supply a descendant path.
func mappingInput(values map[string]interface{}, path string) (interface{}, bool) {
	if path == "" {
		return values, values != nil
	}
	var current interface{} = values
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func (screen *mappingScreen) buildRows() {
	selectedPath := ""
	if screen.cursor < len(screen.rows) {
		selectedPath = screen.rows[screen.cursor].path
	}
	screen.rows = nil
	screen.inside = 0
	source := packagePath(screen.usage.Path)
	target := packagePath(screen.usage.TargetPath)
	_, screen.sourcePresent = mappingInput(screen.pkg.Values, source)
	seen := make(map[string]bool, len(screen.chart.Values))
	var other []mappingRow
	for _, value := range screen.chart.Values {
		path := packagePath(value.Path)
		if seen[path] {
			continue
		}
		seen[path] = true
		row := chartMappingRow(value, screen.pkg, screen.usage)
		if pathWithin(path, target) {
			screen.rows = append(screen.rows, row)
		} else {
			other = append(other, row)
		}
	}
	// Inputs can be supplied even when their path is not in chart discovery.
	// Keep these visible without claiming they are chart defaults or schema-valid.
	packageLeaves("", screen.pkg.Values, func(path string, input interface{}) {
		if !pathWithin(path, source) || path == source && len(screen.chart.Values) > 0 {
			return // A source object is not itself a discovered chart leaf.
		}
		suffix := strings.TrimPrefix(path, source)
		if suffix != "" && !strings.HasPrefix(suffix, ".") && target != "" {
			suffix = "." + suffix
		}
		chartPath := strings.Trim(target+suffix, ".")
		if seen[chartPath] {
			return
		}
		row := chartMappingRow(introspect.ValueUsage{Path: chartPath}, screen.pkg, screen.usage)
		row.undiscovered = true
		screen.rows = append(screen.rows, row)
	})
	screen.inside = len(screen.rows)
	if screen.showOthers {
		screen.rows = append(screen.rows, other...)
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
}

func (m *Model) ensureMappingCursorVisible() {
	if m.mapping == nil {
		return
	}
	visible := max(1, m.windowHeight-13)
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
	add(paneHeadingStyle.Render("CHART VALUES · DECLARED MAPPING, NOT DEPLOYMENT-EFFECTIVE"))
	add(screen.packageLabel() + ": " + screen.pkg.PackageName + "    Chart: " + screen.usage.ComponentName + "/" + screen.usage.ChartName)
	add("Package " + screen.usage.Path + " → chart " + screen.usage.TargetPath)
	add(fmt.Sprintf("In destination: %d    Other chart paths: %s (a toggles)", screen.inside, onOff(screen.showOthers)))
	if height >= 14 {
		add(helpStyle.Render("available = reachable  ·  excluded = explicit  ·  other = outside destination"))
	}
	if !screen.sourcePresent {
		add("Source absent from configured input; chart candidates remain available.")
	}
	add("")
	pathWidth := max(10, inner*45/100)
	statusWidth := 9
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
	available := height - 10
	if !screen.sourcePresent {
		available--
	}
	if height < 14 {
		available++
	}
	available = max(0, available)
	for index := screen.offset; index < len(screen.rows) && index < screen.offset+available; index++ {
		row := screen.rows[index]
		path := row.path
		if row.undiscovered {
			path += " [not discovered]"
		}
		line := fmt.Sprintf("%-*s %-*s %-*s", pathWidth, truncateCell(path, pathWidth), statusWidth, row.status,
			defaultWidth, truncateCell(mappingCell(row.defaultValue, !row.undiscovered && row.defaultValue != nil), defaultWidth))
		if inputWidth > 0 {
			input := "not supplied"
			if row.status == "other" {
				input = "not applicable"
			} else if row.present {
				input = mappingCell(row.input, true)
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
	return paneFrame(width, height, true).Render(strings.TrimSuffix(content.String(), "\n"))
}
