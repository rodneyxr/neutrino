package tui

import (
	"path/filepath"
	"strings"

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
	var plan *valueAuthoring
	if mapping != nil {
		plan = mapping.row.authoring
	} else {
		plan = value.authoring
	}
	if plan == nil {
		return ""
	}
	note := "# Current package input"
	input := plan.value
	switch plan.basis {
	case authoringDefault:
		note = "# Example using the chart default; edit before use"
	case authoringUnknown:
		note = "# No known default; replace <set-value> before use"
		input = "<set-value>"
	}
	masked := value
	masked.Path = plan.path
	masked.IsPackageValue = true
	if mapping != nil && mapping.pkg != nil {
		masked.PackageName = mapping.pkg.PackageName
	}
	input = m.displayValue(masked, input)
	return note + "\n" + packageValuesYAML(plan.path, input)
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
		if i < len(sources) && sources[i] != "" && !filepath.IsAbs(name) {
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
		if filepath.IsAbs(name) {
			source = name
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
	return DisplayItem{Component: m.mapping.chartKey, ValueUsage: row.value}, true
}
