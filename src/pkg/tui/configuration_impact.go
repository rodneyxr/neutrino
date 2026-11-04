package tui

import (
	"fmt"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// configurationImpact compares inspected inputs for one declaration occurrence.
// It never selects a final Helm winner or evaluates deployment-time templates.
type configurationImpact struct {
	chartKey    string
	path        string
	settingPath string
	chartValue  introspect.ValueUsage
	input       interface{}
	present     bool
	status      string
	exclusion   string
	mapping     *valueMapping
}

func (m Model) configurationImpacts(value DisplayItem) []configurationImpact {
	if runtimeReference(value) || value.IsBundleVariable || value.IsPkgVar || value.IsZarfVar || value.Component == "UDS Next bundle" {
		return nil
	}
	if value.IsUDSOverrideValue || value.IsUDSOverrideVar {
		component := value.Component
		if value.IsSummaryItem {
			component = value.SummaryComponent
		}
		target := value.Path
		input, present, status := value.OverrideValue, true, "fixed override"
		if value.IsUDSOverrideVar {
			target = value.UDSVarPath
			input = value.DefaultValue
			present = input != nil
			status = "variable default"
		}
		chartValue := introspect.ValueUsage{Path: target}
		if chart := m.components[component]; chart != nil {
			for _, candidate := range chart.Values {
				if candidate.Path == target {
					chartValue = candidate
					break
				}
			}
		}
		return []configurationImpact{{chartKey: component, path: target, settingPath: value.Path, chartValue: chartValue, input: input, present: present, status: status}}
	}
	if !value.IsPackageValue && !value.IsPackageMapping {
		var rows []configurationImpact
		for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
			if detail.row.status == "other" {
				continue
			}
			row := detail.row
			rows = append(rows, configurationImpact{chartKey: value.Component, path: row.path, settingPath: row.packagePath, chartValue: row.value, input: row.input, present: row.present, status: row.status, exclusion: row.exclusion, mapping: detail.mapping})
		}
		return rows
	}
	mappings := m.configurationMappings(value)
	var rows []configurationImpact
	for _, mapping := range mappings {
		table, err := m.valuePolicy.open(mapping)
		if err != nil {
			source, target := mapping.source, mapping.usage.TargetPath
			if value.IsPackageValue && pathWithin(packagePath(value.Path), mapping.source) {
				source = packagePath(value.Path)
				target = transformPackagePath(source, mapping.source, mapping.target)
				if target == "" {
					target = "."
				}
			}
			var input interface{}
			present := false
			if mapping.pkg != nil {
				input, present = packageValueInput(mapping.pkg.Values, source)
			}
			rows = append(rows, configurationImpact{chartKey: mapping.chartKey, path: target, settingPath: source, input: input, present: present, status: "destination unavailable", mapping: mapping})
			continue
		}
		for _, row := range table.rows[:table.inside] {
			if value.IsPackageValue && !pathWithin(packagePath(row.packagePath), packagePath(value.Path)) {
				continue
			}
			rows = append(rows, configurationImpact{chartKey: mapping.chartKey, path: row.path, settingPath: row.packagePath, chartValue: row.value, input: row.input, present: row.present, status: row.status, exclusion: row.exclusion, mapping: mapping})
		}
	}
	return rows
}

func (m Model) configurationMappings(value DisplayItem) []*valueMapping {
	if runtimeReference(value) || value.IsBundleVariable || value.IsPkgVar || value.IsZarfVar || value.IsUDSOverrideValue || value.IsUDSOverrideVar || value.Component == "UDS Next bundle" {
		return nil
	}
	if !value.IsPackageValue && !value.IsPackageMapping {
		var mappings []*valueMapping
		for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
			if detail.row.status != "other" {
				mappings = append(mappings, detail.mapping)
			}
		}
		return mappings
	}
	var mappings []*valueMapping
	seen := map[*valueMapping]bool{}
	add := func(mapping *valueMapping) {
		if mapping != nil && !seen[mapping] && !mapping.usage.Runtime && mapping.usage.TargetPath != "" {
			seen[mapping] = true
			mappings = append(mappings, mapping)
		}
	}
	add(value.mappingRef)
	for _, route := range value.Routes {
		add(route.mapping)
	}
	// A structural group can own child declarations without a mapping of its own.
	if value.IsPackageValue {
		for _, entry := range m.valuePolicy.catalog().packages {
			if entry.pkg == nil || entry.pkg.PackageName != value.PackageName {
				continue
			}
			candidates := []*valueMapping{entry.mapping}
			for _, route := range entry.routes {
				candidates = append(candidates, route.mapping)
			}
			for _, mapping := range candidates {
				if mapping != nil && pathWithin(mapping.source, packagePath(value.Path)) {
					add(mapping)
				}
			}
		}
	}

	return mappings
}

func (row configurationImpact) title() string {
	return strings.Trim(row.chartKey, "/") + " · .Values" + dottedTracePath(row.path)
}

func (row configurationImpact) state() string {
	switch row.status {
	case "excluded":
		return "Excluded by " + row.exclusion
	case "unresolved", "runtime":
		switch configurationMappingStatus(row.mapping) {
		case introspect.ValueMissing:
			return "Missing configuration input"
		case introspect.ValueParseError:
			return "Input could not be parsed"
		case introspect.ValueUnavailable:
			return "Input unavailable for inspection"
		case introspect.ValueDynamic:
			return "Pending deployment context"
		default:
			return "Unresolved configuration input"
		}
	case "destination unavailable":
		return "Destination chart not inspected"
	case "variable default":
		if row.present {
			return "Declared variable default · deployment may override"
		}
		return "Variable has no supplied default"
	case "fixed override":
		return "Declared fixed bundle override"
	}
	if row.present {
		return "Supplied to this chart path"
	}
	return "No setting input · chart input remains available"
}

func (m Model) impactChartInput(row configurationImpact) string {
	if row.status == "destination unavailable" {
		return "Unavailable"
	}
	if !row.chartValue.DefaultPresent && row.chartValue.DefaultValue == nil {
		return "Not supplied"
	}
	return comparisonPreview(m.impactChartValue(row))
}

func (m Model) impactSettingInput(row configurationImpact) string {
	if !row.present {
		return "Not supplied"
	}
	return comparisonPreview(m.impactSettingValue(row))
}

func (m Model) impactChartValue(row configurationImpact) interface{} {
	value := DisplayItem{ValueUsage: row.chartValue, Component: row.chartKey}
	value.Path = row.path
	return m.displayValue(value, row.chartValue.DefaultValue)
}

// Both configuration owners can declare sensitivity. Apply both boundaries
// before storing a value in a preview; navigation must not reveal raw input.
func (m Model) impactSettingValue(row configurationImpact) interface{} {
	value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: row.settingPath}, IsPackageValue: true}
	if row.mapping != nil && row.mapping.pkg != nil {
		value.PackageName = row.mapping.pkg.PackageName
	}
	if row.status == "fixed override" || row.status == "variable default" {
		value = DisplayItem{ValueUsage: introspect.ValueUsage{Path: row.path}, Component: row.chartKey}
	}
	destination := DisplayItem{ValueUsage: row.chartValue, Component: row.chartKey}
	destination.Path = row.path
	safe := m.displayValue(destination, row.input)
	return m.displayValue(value, safe)
}

func (m Model) impactLines(rows []configurationImpact, limit int, interactive bool) []inspectorLine {
	var lines []inspectorLine
	for i, row := range rows {
		if limit > 0 && i >= limit {
			break
		}
		heading := inspectorLine{text: row.title()}
		if interactive {
			target := DisplayItem{ValueUsage: row.chartValue, Component: row.chartKey}
			target.Path = row.path
			heading.action = &DetailItem{Kind: DetailItemTrace, relatedValue: &target}
		}
		lines = append(lines, heading,
			inspectorLine{label: "Setting", text: row.settingPath},
			inspectorLine{label: "Chart input", text: m.impactChartInput(row)},
			inspectorLine{label: "Setting input", text: m.impactSettingInput(row)},
			inspectorLine{label: "Effect", text: row.state()})
		if row.status == "unresolved" || row.status == "runtime" || row.status == "destination unavailable" {
			for _, reason := range configurationMappingDiagnostics(row.mapping) {
				lines = append(lines, inspectorLine{label: "Reason", text: reason})
			}
		}
		if interactive {
			preview := func(label, title string, value interface{}) {
				if isLongInspectorValue(value) {
					lines = append(lines, inspectorLine{text: label, action: &DetailItem{
						Kind: DetailItemPreview, preview: &inspectorValuePreview{title: title, value: value},
					}})
				}
			}
			if row.present {
				preview("View full setting input", "Setting input · "+row.settingPath, m.impactSettingValue(row))
			}
			if row.status != "destination unavailable" && (row.chartValue.DefaultPresent || row.chartValue.DefaultValue != nil) {
				preview("View full chart input", "Chart input · "+row.title(), m.impactChartValue(row))
			}
		}
	}
	return lines
}

func (m Model) configurationImpactDocument(value DisplayItem, doc inspectorDocument) inspectorDocument {
	rows := m.configurationImpacts(value)
	doc.kind = "Underlying values"
	doc.lines = append(doc.lines, inspectorLine{text: "Inspected inputs; final deployment values are not evaluated.", prose: true})
	doc.lines = append(doc.lines, m.impactLines(rows, 0, true)...)
	if len(rows) == 0 {
		doc.lines = append(doc.lines, inspectorLine{text: "No inspected chart destination is available for this setting."})
	}
	return doc
}

func (m Model) configurationMappingDocument(value DisplayItem, doc inspectorDocument) inspectorDocument {
	doc.kind = "Mapping explanation"
	doc.lines = append(doc.lines, inspectorLine{text: "Routing into chart inputs; final deployment values are not evaluated.", prose: true})
	seen := map[*valueMapping]bool{}
	for _, mapping := range m.configurationMappings(value) {
		if mapping == nil || seen[mapping] {
			continue
		}
		seen[mapping] = true
		doc.lines = append(doc.lines,
			inspectorLine{label: "Package path", text: ".Values" + dottedTracePath(mapping.source)},
			inspectorLine{label: "Chart", text: mapping.chartKey},
			inspectorLine{label: "Chart path", text: ".Values" + dottedTracePath(mapping.target)})
		if status := configurationMappingStatus(mapping); unresolvedStatus(status) {
			doc.lines = append(doc.lines, inspectorLine{label: "Status", text: traceStatus(status)})
			for _, reason := range configurationMappingDiagnostics(mapping) {
				doc.lines = append(doc.lines, inspectorLine{label: "Reason", text: reason})
			}
		}
		if len(mapping.usage.ExcludePaths) > 0 {
			doc.lines = append(doc.lines, inspectorLine{label: "Excluded", text: strings.Join(mapping.usage.ExcludePaths, ", ")})
		}
		if len(mapping.usage.Locations) == 0 {
			doc.lines = append(doc.lines, inspectorLine{label: "Declaration", text: "File and line unavailable"})
		} else {
			var references []DetailItem
			for _, location := range mapping.usage.Locations {
				references = append(references, DetailItem{File: location.File, Location: location, sourceComponent: mapping.chartKey})
			}
			for _, reference := range m.labelInspectorSources(references) {
				_, line := inspectorSourceCoordinates(reference)
				doc.lines = append(doc.lines, inspectorLine{label: "Recorded use", text: sourceCoordinateLabel(reference.sourceDisplayPath, line)})
			}
		}
		detail := DetailItem{Kind: DetailItemMapping, Mapping: mapping.usage, mappingRef: mapping}
		if !value.IsPackageValue && !value.IsPackageMapping {
			candidate := valueMappingDetail{pkg: mapping.pkg, row: mapping.row(value.ValueUsage, packagePath(value.Path)), mapping: mapping}
			detail.packageMapping = &candidate
		}
		doc.lines = append(doc.lines, inspectorLine{text: "Browse chart paths for this rule", action: &detail})
	}
	if len(seen) == 0 {
		doc.lines = append(doc.lines, inspectorLine{text: "No static Package Values mapping was inspected for this setting."})
	}
	return doc
}

func (m Model) configurationImpactSources(value DisplayItem) []DetailItem {
	var items []DetailItem
	for _, row := range m.configurationImpacts(value) {
		for _, location := range row.chartValue.Locations {
			items = append(items, DetailItem{Kind: DetailItemLocation, Location: location, sourceComponent: row.chartKey})
		}
	}
	return items
}

func impactCountLabel(rows []configurationImpact) string {
	supplied := 0
	for _, row := range rows {
		if row.present && row.status != "excluded" {
			supplied++
		}
	}
	noun := "chart paths"
	if len(rows) == 1 {
		noun = "chart path"
	}
	return fmt.Sprintf("%d %s · %d with input", len(rows), noun, supplied)
}

// The separately labeled preview action owns navigation to the full value.
// This text describes only the already masked comparison input.
func comparisonPreview(safe interface{}) string {
	if !isLongInspectorValue(safe) {
		return mappingCell(safe, true)
	}
	switch typed := safe.(type) {
	case map[string]interface{}:
		return fmt.Sprintf("Object · %d fields", len(typed))
	case []interface{}:
		return fmt.Sprintf("List · %d items", len(typed))
	default:
		return fmt.Sprintf("%d characters", len([]rune(formatYAMLValue(safe))))
	}
}
