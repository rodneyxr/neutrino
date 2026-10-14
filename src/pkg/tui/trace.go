package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// traceLine is a presentation projection of immutable inspection evidence. A
// link always names an observed source or a typed value, never an inferred edge.
type traceLine struct {
	text string
	link *DetailItem
}

type traceSection struct {
	title string
	lines []traceLine
}

type traceReturn struct {
	ref         *DisplayItem
	cursor      int
	offset      int
	rowDetail   bool
	schemaShown bool
}

func (m *Model) followTrace(detail DetailItem) bool {
	if detail.Kind != DetailItemTrace || detail.relatedValue == nil {
		return false
	}
	m.traceHistory = append(m.traceHistory, traceReturn{ref: m.detailRef, cursor: m.detailCursor,
		offset: m.detailScrollOffset, rowDetail: m.mappingRowDetail, schemaShown: m.detailSchemaShown})
	target := *detail.relatedValue
	m.detailRef = &target
	m.detailCursor, m.detailScrollOffset = 0, 0
	m.detailSchemaShown = false
	m.buildDetailItems()
	m.ensureDetailCursorVisible()
	return true
}

func (m *Model) returnTrace() bool {
	if len(m.traceHistory) == 0 {
		return false
	}
	last := len(m.traceHistory) - 1
	previous := m.traceHistory[last]
	m.traceHistory = m.traceHistory[:last]
	m.detailRef = previous.ref
	m.mappingRowDetail = previous.rowDetail
	m.detailSchemaShown = previous.schemaShown
	m.buildDetailItems()
	m.detailCursor = clamp(previous.cursor, 0, len(m.detailItems)-1)
	m.detailScrollOffset = previous.offset
	return true
}

type traceCacheKey struct {
	pkg    *introspect.PackageValues
	bundle *introspect.BundleMetadata
	path   string
}

func (m Model) cachedValueTrace(value DisplayItem, sources []introspect.ValueSource, path string) introspect.ValueTrace {
	key := traceCacheKey{path: packagePath(path)}
	if value.IsBundleVariable {
		key.bundle = m.bundle
	} else {
		key.pkg = m.tracePackage(value)
	}
	if m.valuePolicy == nil {
		return introspect.TraceValueSources(sources, path)
	}
	if m.valuePolicy.traces == nil {
		m.valuePolicy.traces = make(map[traceCacheKey]introspect.ValueTrace)
	}
	if trace, found := m.valuePolicy.traces[key]; found {
		return trace
	}
	owner := key
	owner.path = ""
	if m.valuePolicy.tracers == nil {
		m.valuePolicy.tracers = make(map[traceCacheKey]*introspect.ValueTracer)
	}
	tracer := m.valuePolicy.tracers[owner]
	if tracer == nil {
		tracer = introspect.NewValueTracer(sources)
		m.valuePolicy.tracers[owner] = tracer
	}
	trace := tracer.Trace(path)
	m.valuePolicy.traces[key] = trace
	return trace
}

func (m Model) tracePackage(value DisplayItem) *introspect.PackageValues {
	if value.mappingRef != nil {
		return value.mappingRef.pkg
	}
	for i := range m.report.PackageValues {
		pkg := &m.report.PackageValues[i]
		if pkg.PackageName == value.PackageName {
			return pkg
		}
	}
	return nil
}

func (m Model) valueTrace(value DisplayItem) (introspect.ValueTrace, bool) {
	if value.IsBundleVariable && m.bundle != nil && len(m.bundle.VariableSources) > 0 {
		return m.cachedValueTrace(value, m.bundle.VariableSources, value.Path), true
	}
	if value.IsPackageValue || value.IsPackageMapping {
		if pkg := m.tracePackage(value); pkg != nil && len(pkg.Sources) > 0 {
			path := value.Path
			if value.Component == "UDS Next bundle" {
				path = "."
			}
			return m.cachedValueTrace(value, pkg.Sources, path), true
		}
	}
	return introspect.ValueTrace{}, false
}

func (m Model) valueChanged(value DisplayItem) bool {
	if trace, ok := m.valueTrace(value); ok {
		return trace.Changed
	}
	for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
		target := DisplayItem{PackageName: detail.pkg.PackageName, mappingRef: detail.mapping}
		if detail.row.packagePath != "" && detail.row.status != "excluded" &&
			m.cachedValueTrace(target, detail.pkg.Sources, detail.row.packagePath).Changed {
			return true
		}
	}
	return value.IsUDSOverrideValue
}

func (m Model) valueUnresolved(value DisplayItem) bool {
	if value.Runtime {
		return true
	}
	if value.IsBundleVariable && m.bundle != nil {
		return packageSourcesUnresolved(m.bundle.VariableSources) || !value.ValuePresent
	}
	if value.IsPackageValue || value.IsPackageMapping {
		if pkg := m.tracePackage(value); pkg != nil && packageSourcesUnresolved(pkg.Sources) {
			return true
		}
	}
	if value.mappingRef != nil {
		mapping := value.mappingRef
		return mapping.chart == nil || !mapping.sourcePresent || unresolvedStatus(mapping.usage.Status)
	}
	if value.IsPkgVar || value.IsZarfVar || value.IsUDSOverrideVar {
		return value.DefaultValue == nil
	}
	for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
		if detail.row.status == "other" || detail.row.status == "excluded" {
			continue
		}
		if !detail.row.present || detail.mapping.usage.Runtime || unresolvedStatus(detail.mapping.usage.Status) ||
			packageSourcesUnresolved(detail.pkg.Sources) {
			return true
		}
	}
	return false
}

func packageSourcesUnresolved(sources []introspect.ValueSource) bool {
	for _, source := range sources {
		if unresolvedStatus(source.Status) {
			return true
		}
		for _, diagnostic := range source.Diagnostics {
			if unresolvedStatus(diagnostic.Status) {
				return true
			}
		}
	}
	return false
}

func unresolvedStatus(status introspect.ValueStatus) bool {
	return status != "" && status != introspect.ValueResolved
}

func traceStatus(status introspect.ValueStatus) string {
	switch status {
	case introspect.ValueDynamic:
		return "runtime / unsupported evaluation"
	case introspect.ValueMissing:
		return "missing input"
	case introspect.ValueParseError:
		return "parse error"
	case introspect.ValueUnavailable:
		return "unavailable input"
	default:
		return string(status)
	}
}

func (m Model) traceDetailItems(value DisplayItem) []DetailItem {
	var items []DetailItem
	for _, section := range m.valueTraceSections(value) {
		for _, line := range section.lines {
			if line.link != nil {
				items = append(items, *line.link)
			}
		}
	}
	return items
}

// appendValueTrace shares the inspector's line accounting so source and typed
// value links scroll into view using exactly the same keyboard actions.
func (m Model) appendValueTrace(value DisplayItem, width int, focused bool,
	add func(string), section func(string), identity func(string, string), selected func(int)) {
	index := 0
	for _, group := range m.valueTraceSections(value) {
		section(group.title)
		for _, line := range group.lines {
			if line.link == nil {
				add(helpStyle.Render(wrapCell(line.text, max(1, width))))
				continue
			}
			if focused && m.detailCursor == index {
				selected(index)
				add(selectedRowStyle.Width(width).Render("> " + wrapCell(line.text, max(1, width-2))))
			} else {
				add(codeStyle.Render("  " + wrapCell(line.text, max(1, width-2))))
			}
			index++
		}
	}
}

func (m Model) valueTraceSections(value DisplayItem) []traceSection {
	var sections []traceSection
	if value.IsBundleVariable {
		if m.bundle == nil {
			return nil
		}
		sections = append(sections, m.inputHistory(value, m.bundle.VariableSources, value.Path, "BUNDLE .vars")...)
		links := traceSection{title: "TYPED DEPENDENCIES · BUNDLE → PACKAGE"}
		for i := range m.report.PackageValues {
			pkg := &m.report.PackageValues[i]
			for _, source := range pkg.Sources {
				for _, ref := range source.References {
					if ref.Scope != "bundle-vars" || !tracePathsOverlap(value.Path, ref.Path) {
						continue
					}
					target := DisplayItem{ValueUsage: introspect.ValueUsage{Path: pkg.PackageName}, Component: "UDS Next bundle",
						PackageName: pkg.PackageName, IsPackageValue: true, ValueFiles: pkg.Files, ValueFileSources: pkg.FileSources}
					label := pkg.PackageName + " · " + filepath.Base(source.Name) + " reads bundle .vars." + packagePath(ref.Path)
					links.lines = append(links.lines, traceLine{text: label, link: &DetailItem{Kind: DetailItemTrace, relatedValue: &target}})
				}
			}
		}
		if len(links.lines) == 0 {
			links.lines = append(links.lines, traceLine{text: "No statically observed values-file consumer."})
		} else {
			links.lines = append(links.lines, traceLine{text: "File dependencies are observed; individual output leaves are not inferred."})
		}
		links.lines = append(links.lines, traceLine{text: "Bundle .vars render values files; they are separate from Zarf .Values and Helm .Values."},
			traceLine{text: "UDS Next --set input is not inspected. depends_on controls package ordering, not value flow."})
		return append(sections, links)
	}
	if value.IsPackageValue || value.IsPackageMapping {
		pkg := m.tracePackage(value)
		if pkg != nil {
			path := value.Path
			if value.Component == "UDS Next bundle" {
				path = "."
			}
			sections = append(sections, m.inputHistory(value, pkg.Sources, path, "PACKAGE .Values")...)
			sections = append(sections, m.packageInputDependencies(pkg)...)
		}
		boundary := traceSection{title: "TYPED BOUNDARY · PACKAGE → HELM"}
		if value.IsPackageMapping && value.mappingRef != nil {
			boundary.lines = append(boundary.lines, mappingTraceLine(value.mappingRef, value.ValueSource, value.ValueTarget))
			if value.Runtime || unresolvedStatus(value.mappingRef.usage.Status) {
				boundary.lines = append(boundary.lines, traceLine{text: "Unresolved: " + traceStatus(value.mappingRef.usage.Status) + ". " + value.mappingRef.usage.Diagnostic})
			}
		} else {
			for _, route := range value.Routes {
				label := "Zarf .Values." + packagePath(value.Path) + " → " + route.Usage.ComponentName + "/" + route.Usage.ChartName + " Helm .Values." + packagePath(route.Target)
				if route.Status == "excluded" || route.Status == "runtime" {
					label += " · " + route.Status + " (not applied)"
				}
				boundary.lines = append(boundary.lines, traceLine{text: label})
			}
		}
		if len(boundary.lines) == 0 {
			boundary.lines = append(boundary.lines, traceLine{text: "No declared static Helm route for this input."})
		}
		boundary.lines = append(boundary.lines, traceLine{text: "Final Helm result: incomplete. Chart files, templates, legacy variables and overrides may also participate; mappings are inspected declarations."})
		return append(sections, boundary)
	}
	if value.IsPkgVar || value.IsZarfVar || value.IsUDSOverrideValue || value.IsUDSOverrideVar {
		return []traceSection{{title: "LEGACY CONFIGURATION BOUNDARY", lines: []traceLine{
			{text: "Legacy variable substitution and UDS overrides are separate from structured Package Values."},
			{text: "Inspected declaration only. Runtime substitutions and the final Helm result are not evaluated."},
		}}}
	}
	for _, detail := range m.valuePolicy.details(value.Component, value.ValueUsage) {
		if detail.row.status == "other" {
			continue
		}
		pkg := detail.pkg
		if pkg == nil {
			continue
		}
		target := packageValueItem(packageValueEntry{pkg: pkg, path: packagePath(detail.row.packagePath), value: detail.row.input, present: detail.row.present}, "Zarf Package Values")
		for _, entry := range m.valuePolicy.catalog().packages {
			if entry.pkg == pkg && entry.path == packagePath(detail.row.packagePath) && entry.mapping == nil {
				target = packageValueItem(entry, "Zarf Package Values")
				break
			}
		}
		boundary := traceSection{title: "TYPED TRACE · " + pkg.PackageName}
		label := "Zarf .Values." + packagePath(detail.row.packagePath) + " → Helm .Values." + packagePath(value.Path)
		boundary.lines = append(boundary.lines, traceLine{text: label, link: &DetailItem{Kind: DetailItemTrace, relatedValue: &target}})
		if detail.row.status == "excluded" {
			boundary.lines = append(boundary.lines, traceLine{text: "Excluded by " + detail.row.exclusion + "; this route supplies no chart input."})
		} else if detail.row.status == "runtime" || unresolvedStatus(detail.mapping.usage.Status) {
			reason := traceStatus(detail.mapping.usage.Status)
			if reason == "" {
				reason = "runtime / unsupported evaluation"
			}
			boundary.lines = append(boundary.lines, traceLine{text: "Unresolved mapping · " + reason + ". " + detail.mapping.usage.Diagnostic})
		} else if !detail.row.present {
			boundary.lines = append(boundary.lines, traceLine{text: "Missing package input; chart default is not a package input."})
		}
		if len(detail.mapping.exclusions) > 0 {
			boundary.lines = append(boundary.lines, traceLine{text: "Excluded package paths: " + strings.Join(detail.mapping.usage.ExcludePaths, ", ")})
		}
		sections = append(sections, boundary)
		sections = append(sections, m.inputHistory(target, pkg.Sources, detail.row.packagePath, "PACKAGE .Values")...)
		sections = append(sections, m.packageInputDependencies(pkg)...)
	}
	if len(sections) > 0 {
		sections = append(sections, traceSection{title: "HELM RESULT · INCOMPLETE", lines: []traceLine{{text: "No final Helm winner is inferred. Inspected package input is separate from chart defaults, ordered chart files, template evaluation, legacy variables and UDS overrides."}}})
	}
	return sections
}

func mappingTraceLine(mapping *valueMapping, source, target string) traceLine {
	return traceLine{text: "Zarf .Values" + dottedTracePath(source) + " → " + mapping.chartKey + " Helm .Values" + dottedTracePath(target)}
}

func dottedTracePath(path string) string {
	if path = packagePath(path); path != "" {
		return "." + path
	}
	return " (root)"
}

func tracePathsOverlap(a, b string) bool {
	a, b = packagePath(a), packagePath(b)
	return pathWithin(a, b) || pathWithin(b, a)
}

func (m Model) inputHistory(value DisplayItem, sources []introspect.ValueSource, path, scope string) []traceSection {
	if len(sources) == 0 {
		return nil
	}
	trace := m.cachedValueTrace(value, sources, path)
	section := traceSection{title: scope + " · INPUT HISTORY · LOW → HIGH"}
	for index, contribution := range trace.History {
		state := "unchanged input"
		switch {
		case unresolvedStatus(contribution.Status):
			state = traceStatus(contribution.Status)
		case contribution.Shadowed:
			state = "shadowed"
		case contribution.Winner && !trace.Unresolved:
			state = "effective inspected input"
			if trace.Aggregate {
				state = "contributes to inspected object"
			}
		case contribution.Changed:
			state = "changes inspected input"
		}
		label := fmt.Sprintf("%02d  %s · %s · %s", index+1, contribution.Kind, contribution.Name, state)
		line := traceLine{text: label}
		for _, source := range sources {
			if source.ID == contribution.SourceID && traceSourceFile(source) != "" {
				line.link = &DetailItem{Kind: DetailItemTrace, File: traceSourceFile(source)}
				break
			}
		}
		section.lines = append(section.lines, line)
		if !unresolvedStatus(contribution.Status) {
			text := "     removes this path"
			if contribution.AfterPresent {
				text = "     " + mappingCell(m.displayValue(value, contribution.After), true)
			}
			section.lines = append(section.lines, traceLine{text: text})
		}
		for _, diagnostic := range contribution.Diagnostics {
			section.lines = append(section.lines, traceLine{text: "     " + traceStatus(diagnostic.Status) + ": " + diagnostic.Message})
		}
	}
	if trace.Unresolved {
		section.lines = append(section.lines, traceLine{text: "INCOMPLETE INPUT · unknown layers may change this path. No effective inspected winner is claimed."})
	} else if !trace.Present {
		section.lines = append(section.lines, traceLine{text: "MISSING INPUT · absent from all inspected layers."})
	} else {
		section.lines = append(section.lines, traceLine{text: "Effective inspected input: " + mappingCell(m.displayValue(value, trace.Value), true)})
	}
	return []traceSection{section}
}

func traceSourceFile(source introspect.ValueSource) string {
	// Bundle Name identifies the author's actual template; MaterializedFile can
	// point at rendered inspection cache bytes and must not become an edit link.
	if filepath.IsAbs(source.Name) {
		return source.Name
	}
	return source.MaterializedFile
}

func (m Model) packageInputDependencies(pkg *introspect.PackageValues) []traceSection {
	section := traceSection{title: "BUNDLE → PACKAGE · OBSERVED FILE DEPENDENCIES"}
	seen := make(map[string]bool)
	for _, source := range pkg.Sources {
		for _, ref := range source.References {
			if ref.Scope != "bundle-vars" || seen[source.ID+"\x00"+ref.Path] {
				continue
			}
			seen[source.ID+"\x00"+ref.Path] = true
			var input interface{}
			present := false
			if m.bundle != nil {
				input, present = packageValueInput(m.bundle.Variables, packagePath(ref.Path))
			}
			target := DisplayItem{ValueUsage: introspect.ValueUsage{Path: packagePath(ref.Path), DefaultValue: input}, Component: "UDS Next bundle", IsBundleVariable: true, ValuePresent: present}
			label := "bundle .vars." + packagePath(ref.Path) + " → " + filepath.Base(source.Name)
			if ref.Computed {
				label += " · computed expression"
			}
			if unresolvedStatus(ref.Status) {
				label += " · " + traceStatus(ref.Status)
			}
			section.lines = append(section.lines, traceLine{text: label, link: &DetailItem{Kind: DetailItemTrace, relatedValue: &target}})
		}
	}
	if len(section.lines) == 0 {
		return nil
	}
	section.lines = append(section.lines, traceLine{text: "These are source-file dependencies; the inspector does not infer which output leaf an expression produces."})
	return []traceSection{section}
}
