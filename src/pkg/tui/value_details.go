package tui

import (
	"path/filepath"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/goccy/go-yaml"
)

// packageValuesSnippet uses the selected mapping, falling back to the first
// mapping when a source location or related value has focus.
func (m Model) packageValuesSnippet(value DisplayItem) string {
	var mapping *valueMappingDetail
	if m.detailCursor >= 0 && m.detailCursor < len(m.detailItems) {
		mapping = m.detailItems[m.detailCursor].packageMapping
	}
	if mapping == nil {
		for _, detail := range m.detailItems {
			if detail.packageMapping != nil {
				mapping = detail.packageMapping
				break
			}
		}
	}
	if mapping != nil {
		row := mapping.row
		if row.status != "available" || row.packagePath == "" {
			return ""
		}
		input := row.input
		note := "# Current package input"
		if !row.present {
			input = value.DefaultValue
			note = "# Example using the chart default; edit before use"
			if input == nil {
				input = "<set-value>"
				note = "# No known default; replace <set-value> before use"
			}
		}
		return note + "\n" + packageValuesYAML(row.packagePath, input)
	}
	if value.IsPackageValue && value.ValuePresent && value.Component == "Zarf Package Values" && !value.Runtime {
		if value.MappingStatus == "excluded" || value.MappingStatus == "runtime" || value.MappingStatus == "mixed" {
			return ""
		}
		return "# Current package input\n" + packageValuesYAML(value.Path, value.DefaultValue)
	}
	return ""
}

func packageValuesYAML(path string, input interface{}) string {
	var document interface{} = cleanWholeNumbers(input)
	path = packagePath(path)
	if path != "" {
		parts := strings.Split(path, ".")
		for i := len(parts) - 1; i >= 0; i-- {
			document = map[string]interface{}{parts[i]: document}
		}
	}
	contents, err := yaml.Marshal(document)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(contents), "\n")
}

type detailFile struct {
	label  string
	source string
}

type detailFileGroup struct {
	packageName string
	files       []detailFile
}

func detailFileGroups(value DisplayItem, details []DetailItem) []detailFileGroup {
	var groups []detailFileGroup
	seen := make(map[string]bool)
	for _, detail := range details {
		if detail.packageMapping == nil {
			continue
		}
		pkg := detail.packageMapping.pkg
		if len(pkg.Files) == 0 || seen[pkg.PackageName] {
			continue
		}
		seen[pkg.PackageName] = true
		groups = append(groups, detailFileGroup{packageName: pkg.PackageName, files: relativeDetailFiles(pkg.Files, pkg.FileSources)})
	}
	if len(value.ValueFiles) > 0 {
		groups = append(groups, detailFileGroup{files: relativeDetailFiles(value.ValueFiles, value.ValueFileSources)})
	}
	return groups
}

func relativeDetailFiles(names, sources []string) []detailFile {
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = name
		if i < len(sources) && sources[i] != "" {
			paths[i] = sources[i]
		}
		if absolute, err := filepath.Abs(paths[i]); err == nil {
			paths[i] = absolute
		}
	}
	root := commonFileParent(paths)
	files := make([]detailFile, 0, len(names))
	for i, name := range names {
		label := filepath.Clean(name)
		if len(names) > 1 {
			if relative, err := filepath.Rel(root, paths[i]); err == nil {
				label = relative
			}
		}
		source := ""
		if i < len(sources) {
			source = sources[i]
		}
		files = append(files, detailFile{label: label, source: source})
	}
	return files
}

func commonFileParent(paths []string) string {
	if len(paths) == 0 {
		return "."
	}
	root := filepath.Dir(paths[0])
	for _, path := range paths[1:] {
		for {
			relative, err := filepath.Rel(root, path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				return root
			}
			root = parent
		}
	}
	return root
}

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
