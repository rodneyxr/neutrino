package tui

import (
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// detailValue resolves the same chart identity regardless of the entry point.
// A mapping-only supplied path has no invented template locations or default.
func (m Model) detailValue() (DisplayItem, bool) {
	if m.focusPane == FocusInspector && m.detailRef != nil {
		return *m.detailRef, true
	}
	if !m.mappingRowDetail {
		return m.selectedValue()
	}
	if m.mapping == nil || m.mapping.cursor < 0 || m.mapping.cursor >= len(m.mapping.rows) {
		return DisplayItem{}, false
	}
	row := m.mapping.rows[m.mapping.cursor]
	item := DisplayItem{Component: m.mapping.chartKey,
		ValueUsage: introspect.ValueUsage{Path: row.path, DefaultValue: row.defaultValue}}
	for _, value := range m.mapping.chart.Values {
		if packagePath(value.Path) == row.path {
			item.ValueUsage = value
			break
		}
	}
	return item, true
}

type valueMappingDetail struct {
	pkg *introspect.PackageValues
	row mappingRow
}

func chartMappingRow(value introspect.ValueUsage, pkg *introspect.PackageValues, usage introspect.PackageValueUsage) mappingRow {
	path, source, target := packagePath(value.Path), packagePath(usage.Path), packagePath(usage.TargetPath)
	row := mappingRow{path: path, defaultValue: value.DefaultValue, status: "other"}
	if !pathWithin(path, target) {
		return row
	}
	suffix := strings.TrimPrefix(path, target)
	if suffix != "" && !strings.HasPrefix(suffix, ".") && source != "" {
		suffix = "." + suffix
	}
	row.packagePath = "." + strings.Trim(source+suffix, ".")
	row.input, row.present = mappingInput(pkg.Values, packagePath(row.packagePath))
	row.status = "available"
	for _, excluded := range usage.ExcludePaths {
		if pathWithin(packagePath(row.packagePath), packagePath(excluded)) {
			row.status, row.exclusion = "excluded", excluded
			break
		}
	}
	if usage.Runtime {
		row.status = "runtime"
	}
	return row
}

func (m Model) chartMappingDetails(value DisplayItem) []DetailItem {
	var items []DetailItem
	for i := range m.packageValues {
		pkg := &m.packageValues[i]
		for _, usage := range pkg.Usages {
			if usage.TargetPath == "" || usage.ChartName == "" {
				continue
			}
			key := usage.ComponentName + "/" + usage.ChartName
			if m.bundle != nil {
				key = pkg.PackageName + "/" + key
			}
			if value.Component != key {
				// Prefixed reports also occur without bundle metadata in consumers.
				if value.Component != pkg.PackageName+"/"+usage.ComponentName+"/"+usage.ChartName {
					continue
				}
			}
			row := chartMappingRow(value.ValueUsage, pkg, usage)
			items = append(items, DetailItem{Kind: DetailItemMapping, Mapping: usage,
				packageMapping: &valueMappingDetail{pkg: pkg, row: row}})
		}
	}
	return items
}
