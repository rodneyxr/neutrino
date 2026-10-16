package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

type detailPanel uint8

const (
	detailSummary detailPanel = iota
	detailProvenance
	detailSources
	detailSourceFile
	detailDependencies
	detailSchema
	detailFullValue
	detailAuthoring
	detailDescription
	detailReferences
)

type detailPanelReturn struct {
	panel          detailPanel
	cursor, offset int
	source         *DetailItem
}

type inspectorLine struct {
	label, text string
	action      *DetailItem
}

type inspectorDocument struct {
	title, context, kind string
	lines                []inspectorLine
}

func (m *Model) buildDetailItems() {
	value, ok := m.detailValue()
	m.detailItems = nil
	if !ok {
		return
	}
	for _, line := range m.inspectorDocument(value).lines {
		if line.action != nil {
			m.detailItems = append(m.detailItems, *line.action)
		}
	}
}

func (m Model) technicalItems(value DisplayItem) []DetailItem {
	m.detailRef = &value
	m.focusPane = FocusInspector
	m.buildTechnicalDetailItems()
	return m.detailItems
}

func (m *Model) openDetailPanel(panel detailPanel, source *DetailItem) {
	if m.detailPanel == panel && panel != detailSourceFile {
		m.backDetailPanel()
		return
	}
	m.detailPanelHistory = append(m.detailPanelHistory, detailPanelReturn{panel: m.detailPanel, cursor: m.detailCursor, offset: m.detailScrollOffset, source: m.detailSource})
	m.detailPanel, m.detailSource = panel, source
	m.detailCursor, m.detailScrollOffset = 0, 0
	m.detailSchemaShown = panel == detailSchema
	m.detailBundleMode = panel == detailAuthoring
	m.statusMessage = ""
	m.buildDetailItems()
	m.ensureDetailCursorVisible()
}

func (m *Model) backDetailPanel() bool {
	if len(m.detailPanelHistory) == 0 {
		return false
	}
	index := len(m.detailPanelHistory) - 1
	previous := m.detailPanelHistory[index]
	m.detailPanelHistory = m.detailPanelHistory[:index]
	m.detailPanel, m.detailSource = previous.panel, previous.source
	m.detailSchemaShown = previous.panel == detailSchema
	m.detailBundleMode = previous.panel == detailAuthoring
	m.buildDetailItems()
	m.detailCursor, m.detailScrollOffset = clamp(previous.cursor, 0, len(m.detailItems)-1), previous.offset
	return true
}

func (m *Model) resetDetailPanels() {
	m.detailPanel, m.detailSource = detailSummary, nil
	m.detailPanelHistory = nil
	m.detailSchemaShown, m.detailBundleMode = false, false
}

func runtimeReference(value DisplayItem) bool {
	return value.IsPackageMapping && (value.Runtime || value.ValueTarget == "")
}

func referenceRequiresDeployment(value DisplayItem) bool {
	if !runtimeReference(value) || value.mappingRef == nil {
		return false
	}
	for _, location := range value.mappingRef.usage.Locations {
		path := strings.TrimPrefix(value.Path, ".")
		if location.Kind == "setValues" || location.Kind == "runtimeContext" && (strings.HasPrefix(path, "Variables.") || path == "Variables" || strings.HasPrefix(path, "State.") || path == "State") {
			return true
		}
	}
	return false
}

func inspectorKind(value DisplayItem) string {
	switch {
	case runtimeReference(value):
		if referenceRequiresDeployment(value) {
			return "Zarf runtime reference"
		}
		return "Zarf template reference"
	case value.IsBundleVariable:
		return "Bundle variable"
	case value.IsPackageMapping:
		return "Package Values mapping"
	case value.IsPackageValue:
		if value.Component == "UDS Next bundle" {
			return "Bundle values-file input"
		}
		return "Package value"
	case value.IsPkgVar || value.IsZarfVar:
		return "Legacy Zarf variable"
	case value.IsUDSOverrideValue:
		return "Legacy UDS override"
	case value.IsUDSOverrideVar:
		return "Legacy UDS variable"
	case value.Wildcard:
		return "Helm dynamic reference"
	default:
		return "Helm value"
	}
}

func inspectorContext(value DisplayItem) string {
	key := value.Component
	if value.IsSummaryItem {
		key = value.SummaryComponent
	}
	pkg, component, chart := parseComponentKey(key)
	if value.PackageName != "" {
		pkg = value.PackageName
	}
	if value.IsPackageMapping {
		component, chart = value.MappingComponent, value.MappingChart
	} else if value.IsPackageValue || value.IsBundleVariable || value.IsPkgVar {
		component, chart = "", ""
	}
	parts := []string{}
	for _, part := range []string{pkg, component, chart} {
		if part != "" && (len(parts) == 0 || parts[len(parts)-1] != part) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " › ")
}

func declaredSchema(value DisplayItem) map[string]interface{} {
	schema, _ := value.Schema.(map[string]interface{})
	return schema
}

func (m Model) selectedValueSchema(value DisplayItem) interface{} {
	if value.ValueSchema != nil {
		return value.ValueSchema
	}
	if result := m.components[value.Component]; result != nil {
		var selected interface{}
		longest := -1
		for _, schema := range result.ValuesSchemas {
			prefix, path := packagePath(schema.ValuePrefix), packagePath(value.Path)
			if schema.Schema != nil && (prefix == "" || path == prefix || strings.HasPrefix(path, prefix+".")) && len(prefix) > longest {
				selected, longest = schema.Schema, len(prefix)
			}
		}
		if selected != nil {
			return selected
		}
	}
	return value.Schema
}

func (m Model) hasValueHistory(value DisplayItem) bool {
	if runtimeReference(value) {
		return false
	}
	if value.IsBundleVariable {
		return m.bundle != nil && len(m.bundle.VariableSources) > 0
	}
	if value.IsPackageValue || value.IsPackageMapping {
		pkg := m.tracePackage(value)
		return pkg != nil && len(pkg.Sources) > 0
	}
	if value.IsZarfVar || value.IsPkgVar || value.IsUDSOverrideValue || value.IsUDSOverrideVar {
		return false
	}
	for _, mapping := range m.valuePolicy.details(value.Component, value.ValueUsage) {
		if mapping.row.status != "other" && mapping.pkg != nil && len(mapping.pkg.Sources) > 0 {
			return true
		}
	}
	return false
}

func (m Model) inspectorDocument(value DisplayItem) inspectorDocument {
	doc := inspectorDocument{title: value.Path, context: inspectorContext(value), kind: inspectorKind(value)}
	if m.detailPanel != detailSummary {
		return m.inspectorSecondaryDocument(value, doc)
	}
	field := func(label, text string) { doc.lines = append(doc.lines, inspectorLine{label: label, text: text}) }
	action := func(label string, detail DetailItem) {
		doc.lines = append(doc.lines, inspectorLine{text: label, action: &detail})
	}
	panel := func(label string, destination detailPanel) {
		action(label, DetailItem{Kind: DetailItemPanel, panel: destination})
	}
	schema := declaredSchema(value)
	if kind, ok := schema["type"].(string); ok && kind != "" {
		doc.kind += " · " + kind
	}
	switch {
	case runtimeReference(value):
		if referenceRequiresDeployment(value) {
			field("Value", "Requires deployment context")
		} else {
			field("Value", "Expression not evaluated")
		}
	case value.Wildcard:
		field("Value", "Dynamic path · not evaluated")
	case value.IsPackageMapping:
		field("Source", ".Values"+dottedTracePath(value.ValueSource))
		field("Target", "Helm .Values"+dottedTracePath(value.ValueTarget))
	case value.IsPackageValue && value.Component == "UDS Next bundle":
		field("Values files", fmt.Sprintf("%d declared", len(value.ValueFiles)))
		field("Status", m.bundleFilesState(value))
	case value.IsPackageValue || value.IsBundleVariable:
		if !value.ValuePresent {
			field("Input", "Not supplied")
		} else if m.valueUnresolved(value) {
			field("Known input", m.compactInput(value, value.DefaultValue)+" · incomplete")
		} else {
			field("Inspected input", m.compactInput(value, value.DefaultValue))
		}
	case value.IsUDSOverrideValue:
		field("Bundle override", m.compactInput(value, value.OverrideValue))
	case value.DefaultPresent || value.DefaultValue != nil:
		field("Default", m.compactInput(value, value.DefaultValue))
	default:
		field("Default", "Not supplied")
	}
	if !runtimeReference(value) && !value.Wildcard {
		if annotation, exists := schema["default"]; exists {
			preview := m.compactInput(value, annotation)
			if isLongInspectorValue(m.displayValue(value, annotation)) {
				preview = "See schema"
			}
			field("Schema default", preview+" · annotation only")
		}
		if value.SchemaRequired {
			field("Schema", "Required in parent object")
		}
	}
	description := inspectorDescription(value)
	if description != "" {
		field("Description", description)
		width := m.inspectorWidth()
		innerWidth := max(1, width-4)
		labelWidth := min(15, max(8, innerWidth/3))
		if strings.Count(wrapCell(description, max(1, innerWidth-labelWidth-1)), "\n") >= 2 {
			panel("Read description", detailDescription)
		}
	}
	technical := m.technicalItems(value)
	if value.IsPackageMapping && !runtimeReference(value) && value.mappingRef != nil {
		action("Explore mapped settings", DetailItem{Kind: DetailItemMapping, Mapping: value.mappingRef.usage, mappingRef: value.mappingRef})
	}
	for _, detail := range technical {
		if detail.Kind != DetailItemMapping || detail.Mapping.TargetPath == "" || detail.Mapping.Runtime {
			continue
		}
		label := "Used by " + detail.Mapping.ComponentName + "/" + detail.Mapping.ChartName + " · " + detail.Mapping.TargetPath
		if detail.packageMapping != nil {
			row := detail.packageMapping.row
			if row.status == "other" {
				continue
			}
			state := "not supplied"
			if row.status == "excluded" {
				state = "excluded"
			} else if row.present {
				state = m.compactInput(DisplayItem{ValueUsage: introspect.ValueUsage{Path: row.packagePath}, IsPackageValue: true, PackageName: detail.packageMapping.pkg.PackageName}, row.input)
			}
			label = "Package input: " + state + " · " + row.packagePath
		} else {
			for _, route := range value.Routes {
				if route.mapping == detail.mappingRef && route.Target != "" {
					label = "Used by " + detail.Mapping.ComponentName + "/" + detail.Mapping.ChartName + " · " + route.Target
				}
			}
		}
		action(label, detail)
	}
	if m.canViewFullValue(value) && isLongInspectorValue(m.displayValue(value, inspectorRawValue(value))) {
		panel("View full value", detailFullValue)
	}
	if count := len(m.inspectorSources(value)); count > 0 {
		panel(fmt.Sprintf("Sources (%d)", count), detailSources)
	}
	if m.hasValueHistory(value) {
		panel("Input history", detailProvenance)
	}
	if m.hasFileDependencies(value) {
		label := "Other inputs in source files"
		if value.IsBundleVariable {
			label = "Values files using this variable"
		}
		panel(label, detailDependencies)
	}
	if !runtimeReference(value) && m.selectedValueSchema(value) != nil {
		panel("Schema", detailSchema)
	}
	if value.Component == "UDS Next bundle" && value.IsPackageValue {
		panel("Package references", detailReferences)
	} else {
		for _, detail := range technical {
			if detail.Kind == DetailItemMapping && detail.Mapping.Runtime {
				panel("Observed uses", detailReferences)
				break
			}
		}
	}
	for _, detail := range technical {
		if detail.Kind == DetailItemCrossRef {
			action(strings.TrimPrefix(detail.Label, "→ "), detail)
		}
	}
	return doc
}

func (m Model) hasFileDependencies(value DisplayItem) bool {
	if runtimeReference(value) {
		return false
	}
	if value.IsBundleVariable {
		for _, pkg := range m.report.PackageValues {
			for _, source := range pkg.Sources {
				for _, ref := range source.References {
					if ref.Scope == "bundle-vars" && tracePathsOverlap(value.Path, ref.Path) {
						return true
					}
				}
			}
		}
		return false
	}
	return (value.IsPackageValue || value.IsPackageMapping) && len(m.packageInputDependencies(m.tracePackage(value))) > 0
}

func inspectorDescription(value DisplayItem) string {
	if value.UDSVarDescription != "" {
		return value.UDSVarDescription
	}
	if value.PkgVarDesc != "" {
		return value.PkgVarDesc
	}
	description, _ := declaredSchema(value)["description"].(string)
	return description
}

func (m Model) inspectorFooter(width int) string {
	text := "enter open  esc back"
	if len(m.detailItems) == 0 {
		text = "j/k scroll  esc back"
	}
	if width < 60 {
		return text + "  ? help"
	}
	text += "  tab panes"
	if value, ok := m.detailValue(); ok && m.detailPanel == detailSummary {
		if m.canViewFullValue(value) {
			text += "  v value"
		}
		if m.hasValueHistory(value) {
			text += "  t history"
		}
		if !runtimeReference(value) && m.selectedValueSchema(value) != nil {
			text += "  s schema"
		}
	}
	return text + "  ? help  q quit"
}

func inspectorRawValue(value DisplayItem) interface{} {
	if value.IsUDSOverrideValue {
		return value.OverrideValue
	}
	return value.DefaultValue
}

func (m Model) canViewFullValue(value DisplayItem) bool {
	if runtimeReference(value) || value.IsPackageMapping || value.Wildcard {
		return false
	}
	if value.IsPackageValue || value.IsBundleVariable {
		return value.ValuePresent
	}
	return value.DefaultPresent || value.DefaultValue != nil || value.IsUDSOverrideValue
}

func isLongInspectorValue(value interface{}) bool {
	text := formatYAMLValue(value)
	return strings.Contains(strings.TrimSpace(text), "\n") || lipgloss.Width(text) > 56
}

func (m Model) compactInput(value DisplayItem, input interface{}) string {
	input = m.displayValue(value, input)
	if isLongInspectorValue(input) {
		text := formatYAMLValue(input)
		return fmt.Sprintf("%d characters · v to view", len([]rune(text)))
	}
	return mappingCell(input, true)
}

func (m Model) inspectorContent(width int, focused bool) (string, int) {
	value, ok := m.detailValue()
	if !ok {
		return "", -1
	}
	doc := m.inspectorDocument(value)
	var content strings.Builder
	lineNumber, selected, actionIndex := 0, -1, 0
	add := func(text string) {
		content.WriteString(text + "\n")
		lineNumber += strings.Count(text, "\n") + 1
	}
	add(detailTitleStyle.Render(wrapCell(doc.title, width)))
	if doc.context != "" {
		add(helpStyle.Render(clipLinesAt(wrapCell(doc.context, width), 2, 0)))
	}
	add(kindStyle.Render(wrapCell(doc.kind, width)))
	compact := m.detailPanel == detailSummary && m.windowHeight > 0 && m.windowHeight <= 14
	if !compact {
		add("")
	}
	hadAction := false
	for _, row := range doc.lines {
		if row.action != nil {
			if !hadAction && !compact {
				add("")
				hadAction = true
			}
			text := truncateCell(row.text, max(1, width-2))
			if focused && actionIndex == m.detailCursor {
				selected = lineNumber
				add(selectedRowStyle.Width(width).Render("> " + text))
			} else {
				add(codeStyle.Render("  " + text))
			}
			actionIndex++
			continue
		}
		if row.label == "" {
			add(wrapCell(row.text, width))
			continue
		}
		labelWidth := min(15, max(8, width/3))
		if width < 44 && row.label == "Value" {
			labelWidth = 8
		}
		text := wrapCell(row.text, max(1, width-labelWidth-1))
		if m.detailPanel == detailSummary {
			text = clipLinesAt(text, 2, 0)
		}
		lines := strings.Split(text, "\n")
		add(fieldLabelStyle.Render(fmt.Sprintf("%-*s", labelWidth, row.label)) + " " + lines[0])
		for _, line := range lines[1:] {
			add(strings.Repeat(" ", labelWidth+1) + line)
		}
	}
	return strings.TrimSuffix(content.String(), "\n"), selected
}

func friendlySourceLabel(pkg string, source introspect.ValueSource, index int) string {
	role := "values file"
	switch source.Kind {
	case "bundle-values":
		role = "bundle values file"
	case "packaged-default":
		role = "packaged defaults"
	case "operator-file":
		role = "operator values file"
	case "bundle-defaults":
		role = "bundle defaults"
	case "bundle-config":
		role = "bundle config"
	}
	label := fmt.Sprintf("%s %d", role, index+1)
	if pkg != "" {
		label = pkg + " / " + label
	}
	return label
}

func sourceRole(location introspect.ValueUsageLocation) string {
	switch location.Kind {
	case "schema":
		return "Values schema"
	case "values", "default":
		return "Chart defaults"
	case "value":
		if filepath.Base(location.File) == "values.yaml" {
			return "Chart defaults"
		}
		return "Template"
	case "runtimeContext", "runtimeTemplate", "packageValue", "templatedValues":
		return "Chart values template"
	case "setValues":
		return "Runtime action output"
	case "bundleVariable", "bundleValuesTemplate":
		return "Bundle values template"
	default:
		return "Template"
	}
}

func (m Model) inspectorSources(value DisplayItem) []DetailItem {
	var sources []DetailItem
	seen := map[string]bool{}
	for _, detail := range m.technicalItems(value) {
		if detail.Kind != DetailItemLocation && detail.Kind != DetailItemFile && !(detail.Kind == DetailItemTrace && detail.File != "") {
			continue
		}
		file := detail.File
		if detail.Kind == DetailItemLocation {
			file = detail.Location.File
		}
		key := fmt.Sprintf("%s:%d:%d", file, detail.Location.Line, detail.Location.Column)
		if seen[key] {
			continue
		}
		seen[key] = true
		label := sourceRole(detail.Location)
		if detail.sourceComponent != "" {
			_, component, chart := parseComponentKey(detail.sourceComponent)
			label = strings.Trim(component+"/"+chart, "/") + " · " + label
		}
		if detail.Location.Line > 0 {
			label += fmt.Sprintf(" · line %d", detail.Location.Line)
		} else {
			label += " · location unavailable"
		}
		if detail.Kind != DetailItemLocation {
			label = "Values source"
			if pkg := m.tracePackage(value); pkg != nil {
				for i, source := range pkg.Sources {
					if traceSourceFile(source) == file {
						label = friendlySourceLabel(pkg.PackageName, source, i)
						break
					}
				}
			}
		}
		detail.Kind, detail.File, detail.Label = DetailItemSource, file, label
		sources = append(sources, detail)
	}
	return sources
}

func (m Model) inspectorSecondaryDocument(value DisplayItem, doc inspectorDocument) inspectorDocument {
	field := func(label, text string) { doc.lines = append(doc.lines, inspectorLine{label: label, text: text}) }
	action := func(label string, detail DetailItem) {
		doc.lines = append(doc.lines, inspectorLine{text: label, action: &detail})
	}
	switch m.detailPanel {
	case detailDescription:
		doc.kind = "Description"
		field("", inspectorDescription(value))
	case detailReferences:
		doc.kind = "Package references"
		if value.Component != "UDS Next bundle" {
			doc.kind = "Observed uses"
			for _, detail := range m.technicalItems(value) {
				if detail.Kind != DetailItemMapping || !detail.Mapping.Runtime {
					continue
				}
				field("Used in", strings.Trim(detail.Mapping.ComponentName+"/"+detail.Mapping.ChartName, "/"))
				field("Evaluation", "runtime or template expression")
				if detail.Mapping.TargetPath != "" {
					field("Declared target", detail.Mapping.TargetPath)
				}
			}
		} else if m.bundle != nil {
			field("Bundle", strings.TrimSpace(m.bundle.Name+" "+m.bundle.Version))
			for _, pkg := range m.bundle.Packages {
				if pkg.ID != value.PackageName {
					continue
				}
				if pkg.Source != "" {
					field("Source", pkg.Source)
				}
				if pkg.Namespace != "" {
					field("Namespace", pkg.Namespace)
				}
				if len(pkg.DependsOn) > 0 {
					field("Starts after", strings.Join(pkg.DependsOn, ", "))
				}
				if len(pkg.OptionalComponents) > 0 {
					field("Components", strings.Join(pkg.OptionalComponents, ", "))
				}
				for i, file := range pkg.ValuesFiles {
					action(fmt.Sprintf("Bundle values file %d", i+1), DetailItem{Kind: DetailItemSource, File: file, Label: "Bundle values template"})
				}
			}
		}
	case detailSources:
		doc.kind = "Sources"
		for _, source := range m.inspectorSources(value) {
			action(source.Label, source)
		}
	case detailSourceFile:
		doc.kind = "Source details"
		if source := m.detailSource; source != nil {
			field("Role", source.Label)
			field("File", source.File)
			if source.Location.Line > 0 {
				field("Line", fmt.Sprint(source.Location.Line))
			} else {
				field("Location", "Unavailable")
			}
			if sourceFileAvailable(source.File) {
				action("Open file in editor", DetailItem{Kind: DetailItemFile, File: source.File, Location: source.Location})
			} else {
				field("Editor", "Source not available locally")
			}
		}
	case detailSchema:
		doc.kind = "Schema · annotations, not validation"
		field("", formatYAMLValue(traceSafeSchema(m.selectedValueSchema(value))))
	case detailFullValue:
		doc.kind = "Inspected value"
		field("", formatYAMLValue(m.displayValue(value, inspectorRawValue(value))))
	case detailAuthoring:
		doc.kind = "Authoring example · review before use"
		if snippet := m.packageValuesSnippet(value); snippet != "" {
			field("", snippet)
		}
		if snippet := m.generateBundleSnippetForItem(value); snippet != "" {
			field("", snippet)
		}
		if len(doc.lines) == 0 {
			field("", "No authoring example is available for this reference.")
		}
	case detailDependencies:
		doc.kind = "Other inputs referenced in source files"
		if value.IsBundleVariable {
			doc.kind = "Values files using this variable"
		}
		groups := m.packageInputDependencies(m.tracePackage(value))
		if value.IsBundleVariable {
			groups = m.valueTraceSections(value)
			if len(groups) > 0 {
				groups = groups[len(groups)-1:]
			}
		}
		for _, group := range groups {
			for _, line := range group.lines {
				if line.link != nil {
					action(line.text, *line.link)
				}
			}
		}
	case detailProvenance:
		doc.kind = "Input history · low to high precedence"
		if !m.hasValueHistory(value) {
			field("", "No input history applies to this runtime reference.")
			break
		}
		for _, group := range m.valueTraceSections(value) {
			if !strings.Contains(group.title, "INPUT HISTORY") {
				continue
			}
			for _, line := range group.lines {
				if line.link != nil {
					detail := *line.link
					detail.Kind, detail.Label = DetailItemSource, line.text
					action(line.text, detail)
				} else {
					field("", line.text)
				}
			}
		}
		if value.IsBundleVariable || len(m.packageInputDependencies(m.tracePackage(value))) > 0 {
			action("File dependencies", DetailItem{Kind: DetailItemPanel, panel: detailDependencies})
		}
	}
	return doc
}

func sourceFileAvailable(file string) bool {
	if !filepath.IsAbs(file) {
		return false
	}
	info, err := os.Stat(file)
	return err == nil && !info.IsDir()
}
