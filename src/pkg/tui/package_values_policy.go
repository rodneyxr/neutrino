package tui

import (
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// packageValuesPolicy borrows an immutable inspection report. The catalog and
// declaration references are built once; chart-first tables are built on demand.
type packageValuesPolicy struct {
	entries packageValuesCatalog
	byChart map[string][]*valueMapping
	tables  map[*valueMapping]*mappingTable
	traces  map[traceCacheKey]introspect.ValueTrace
	tracers map[traceCacheKey]*introspect.ValueTracer
}

type packageValuesCatalog struct {
	packages []packageValueEntry
	charts   map[string][]packageValueEntry
}

type packageValueEntry struct {
	pkg            *introspect.PackageValues
	path           string
	value          interface{}
	present        bool
	schema         interface{}
	schemaRequired bool
	mapping        *valueMapping
	routes         []packageRoute
	status         string
	authoring      *valueAuthoring
}

type packageRoute struct {
	Usage   introspect.PackageValueUsage
	Status  string
	Target  string
	mapping *valueMapping
}

type mappingRow struct {
	path         string
	defaultValue interface{}
	packagePath  string
	input        interface{}
	present      bool
	status       string
	exclusion    string
	undiscovered bool
	value        introspect.ValueUsage
	authoring    *valueAuthoring
}

type valueMappingDetail struct {
	pkg     *introspect.PackageValues
	row     mappingRow
	mapping *valueMapping
}

type valueMapping struct {
	pkg             *introspect.PackageValues
	usage           introspect.PackageValueUsage
	chart           *introspect.IntrospectResult
	chartKey        string
	source          string
	target          string
	exclusions      []string
	leaves          []packageValueLeaf
	referencesInput bool
	sourcePresent   bool
	hasLeaf         bool
}

type mappingTable struct {
	mapping       *valueMapping
	rows          []mappingRow
	inside        int
	sourcePresent bool
}

type authoringBasis uint8

const (
	authoringSupplied authoringBasis = iota
	authoringDefault
	authoringUnknown
)

type valueAuthoring struct {
	path  string
	value interface{}
	basis authoringBasis
}

type packageValueLeaf struct {
	path           string
	value          interface{}
	present        bool
	declared       bool
	schema         interface{}
	schemaRequired bool
}

var emptyPackageValuesCatalog packageValuesCatalog

func newPackageValuesPolicy(report introspect.InspectionReport) *packageValuesPolicy {
	policy := &packageValuesPolicy{
		entries: packageValuesCatalog{charts: make(map[string][]packageValueEntry, len(report.Components))},
		byChart: make(map[string][]*valueMapping),
		tables:  make(map[*valueMapping]*mappingTable),
		traces:  make(map[traceCacheKey]introspect.ValueTrace),
		tracers: make(map[traceCacheKey]*introspect.ValueTracer),
	}
	ownersByChart := make(map[string][]*valueMapping)
	for i := range report.PackageValues {
		pkg := &report.PackageValues[i]
		leaves := packageValueInventory(pkg)
		// Addresses in this fixed-size slice identify declaration occurrences,
		// including otherwise identical declarations in the same package.
		mappings := make([]valueMapping, len(pkg.Usages))
		for j, usage := range pkg.Usages {
			key := usage.ComponentName + "/" + usage.ChartName
			if pkg.PackageName != "" {
				prefixed := pkg.PackageName + "/" + key
				if _, exists := report.Components[prefixed]; exists {
					key = prefixed
				}
			}
			mapping := &mappings[j]
			*mapping = compileValueMapping(pkg, usage, key, report.Components[key], leaves)
			ownersByChart[key] = append(ownersByChart[key], mapping)
			if usage.TargetPath != "" && usage.ChartName != "" {
				policy.byChart[key] = append(policy.byChart[key], mapping)
			}
		}
		for _, leaf := range leaves {
			entry := packageValueEntry{pkg: pkg, path: leaf.path, value: leaf.value, present: leaf.present,
				schema: leaf.schema, schemaRequired: leaf.schemaRequired}
			routePath := packagePath(leaf.path)
			for j := range mappings {
				mapping := &mappings[j]
				if route, applies := mapping.route(routePath); applies {
					mapping.hasLeaf = true
					entry.routes = append(entry.routes, route)
				}
			}
			entry.status = aggregatePackageRoutes(entry.routes)
			if entry.present && entry.status != "excluded" && entry.status != "runtime" && entry.status != "mixed" {
				entry.authoring = &valueAuthoring{path: leaf.path, value: leaf.value, basis: authoringSupplied}
			}
			if packageSourcesUnresolved(pkg.Sources) {
				entry.authoring = nil
			}
			policy.entries.packages = append(policy.entries.packages, entry)
		}
		for j := range mappings {
			mapping := &mappings[j]
			usage := mapping.usage
			if (usage.Runtime || usage.TargetPath == "" || usage.ChartName == "") && mapping.hasLeaf {
				continue
			}
			policy.entries.packages = append(policy.entries.packages, packageValueEntry{
				pkg: pkg, path: usage.Path, mapping: mapping,
			})
		}
		if len(leaves) == 0 && (len(pkg.Files) > 0 || len(pkg.Schema) > 0) {
			policy.entries.packages = append(policy.entries.packages, packageValueEntry{
				pkg: pkg, path: "(package values)",
			})
		}
	}
	for key, chart := range report.Components {
		if chart == nil || len(chart.PackageValueMappings) == 0 {
			continue
		}
		// Match each chart occurrence once, so duplicate declarations retain
		// distinct identities instead of all pointing at the first match.
		used := make(map[*valueMapping]bool)
		entries := make([]packageValueEntry, 0, len(chart.PackageValueMappings))
		for _, usage := range chart.PackageValueMappings {
			var owner, firstMatch *valueMapping
			for _, candidate := range ownersByChart[key] {
				if !sameValueDeclaration(candidate.usage, usage) {
					continue
				}
				if firstMatch == nil {
					firstMatch = candidate
				}
				if !used[candidate] {
					owner = candidate
					used[candidate] = true
					break
				}
			}
			if owner == nil {
				owner = firstMatch
			}
			var mapping *valueMapping
			if owner != nil && owner.usage.Runtime == usage.Runtime && slices.Equal(owner.usage.Locations, usage.Locations) {
				mapping = owner
			} else if owner != nil {
				// Retain chart-side spelling and locations without losing the
				// package occurrence's ownership and compiled routing.
				chartMapping := *owner
				chartMapping.usage = usage
				mapping = &chartMapping
			} else {
				chartMapping := compileValueMapping(nil, usage, key, chart, nil)
				mapping = &chartMapping
			}
			entries = append(entries, packageValueEntry{pkg: mapping.pkg, path: usage.Path, mapping: mapping})
		}
		policy.entries.charts[key] = entries
	}
	return policy
}

func (policy *packageValuesPolicy) catalog() *packageValuesCatalog {
	if policy == nil {
		return &emptyPackageValuesCatalog
	}
	return &policy.entries
}

func compileValueMapping(pkg *introspect.PackageValues, usage introspect.PackageValueUsage, key string, chart *introspect.IntrospectResult, leaves []packageValueLeaf) valueMapping {
	mapping := valueMapping{pkg: pkg, usage: usage, chart: chart, chartKey: key,
		source: packagePath(usage.Path), target: packagePath(usage.TargetPath), leaves: leaves,
		referencesInput: referencesPackageInput(usage)}
	if len(usage.ExcludePaths) > 0 {
		mapping.exclusions = make([]string, len(usage.ExcludePaths))
		for i, excluded := range usage.ExcludePaths {
			mapping.exclusions[i] = packagePath(excluded)
		}
	}
	if pkg != nil && mapping.referencesInput {
		_, mapping.sourcePresent = packageValueInput(pkg.Values, mapping.source)
	}
	return mapping
}

func sameValueDeclaration(a, b introspect.PackageValueUsage) bool {
	if a.Path != b.Path || a.ComponentName != b.ComponentName || a.ChartName != b.ChartName ||
		a.TargetPath != b.TargetPath || len(a.ExcludePaths) != len(b.ExcludePaths) ||
		referencesPackageInput(a) != referencesPackageInput(b) {
		return false
	}
	for i, excluded := range a.ExcludePaths {
		if excluded != b.ExcludePaths[i] {
			return false
		}
	}
	return true
}

// Only declared mappings and observed .Values lookups identify package input.
// Other template contexts can have identical path text; generic templates use
// "." as an observation label, not as a reference to every package value.
func referencesPackageInput(usage introspect.PackageValueUsage) bool {
	if usage.TargetPath != "" {
		return true
	}
	if len(usage.Locations) == 0 {
		return false
	}
	for _, location := range usage.Locations {
		switch location.Kind {
		case "packageValue", "field", "range", "with", "index", "value", "variable":
			// The package template reader uses the .Values walker, including
			// literal index lookups (value) and resolved aliases (variable).
		default:
			return false
		}
	}
	return true
}

func (mapping *valueMapping) route(path string) (packageRoute, bool) {
	if !mapping.referencesInput || !pathWithin(path, mapping.source) {
		return packageRoute{}, false
	}
	route := packageRoute{Usage: mapping.usage, Status: "mapped", mapping: mapping}
	if mapping.usage.Runtime || mapping.usage.TargetPath == "" {
		route.Status = "runtime"
	} else if _, excluded := mapping.exclusion(path); excluded {
		route.Status = "excluded"
	} else {
		route.Target = transformPackagePath(path, mapping.source, mapping.target)
		if route.Target == "" {
			route.Target = "."
		}
		if unresolvedStatus(mapping.usage.Status) || mapping.pkg != nil && packageSourcesUnresolved(mapping.pkg.Sources) {
			route.Status = "unresolved"
		}
	}
	return route, true
}

func aggregatePackageRoutes(routes []packageRoute) string {
	if len(routes) == 0 {
		return "unmapped"
	}
	status := routes[0].Status
	for _, route := range routes[1:] {
		if route.Status != status {
			return "mixed"
		}
	}
	return status
}

func (mapping *valueMapping) exclusion(path string) (string, bool) {
	for i, excluded := range mapping.exclusions {
		if pathWithin(path, excluded) {
			return mapping.usage.ExcludePaths[i], true
		}
	}
	return "", false
}

// transformPackagePath is shared by both directions. Its caller has already
// checked containment, so prefix removal always respects whole path segments.
func transformPackagePath(path, from, to string) string {
	suffix := strings.TrimPrefix(path, from)
	suffix = strings.TrimPrefix(suffix, ".")
	if to == "" {
		return suffix
	}
	if suffix == "" {
		return to
	}
	return to + "." + suffix
}

func (policy *packageValuesPolicy) open(mapping *valueMapping) (*mappingTable, error) {
	if policy == nil || mapping == nil || mapping.pkg == nil {
		return nil, errors.New("Package for this mapping is unavailable")
	}
	if mapping.chart == nil {
		return nil, errors.New("Destination chart for this mapping is unavailable")
	}
	if table := policy.tables[mapping]; table != nil {
		return table, nil
	}
	table := &mappingTable{mapping: mapping, sourcePresent: mapping.sourcePresent,
		rows: make([]mappingRow, 0, len(mapping.chart.Values)+len(mapping.leaves))}
	seen := make(map[string]bool, len(mapping.chart.Values)+len(mapping.leaves))
	for _, value := range mapping.chart.Values {
		path := packagePath(value.Path)
		if seen[path] {
			continue
		}
		seen[path] = true
		if pathWithin(path, mapping.target) {
			table.rows = append(table.rows, mapping.row(value, path))
		}
	}
	for _, leaf := range mapping.leaves {
		if !pathWithin(leaf.path, mapping.source) || leaf.path == mapping.source && !leaf.declared && len(mapping.chart.Values) > 0 {
			continue
		}
		path := transformPackagePath(leaf.path, mapping.source, mapping.target)
		if seen[path] {
			continue
		}
		seen[path] = true
		row := mapping.row(introspect.ValueUsage{Path: path}, path)
		row.undiscovered = true
		table.rows = append(table.rows, row)
	}
	table.inside = len(table.rows)
	// A second borrowed pass avoids allocating/copying a separate outside-row
	// buffer. Deleting seen outside paths retains first-occurrence ordering.
	for _, value := range mapping.chart.Values {
		path := packagePath(value.Path)
		if !pathWithin(path, mapping.target) && seen[path] {
			table.rows = append(table.rows, mapping.row(value, path))
			delete(seen, path)
		}
	}
	policy.tables[mapping] = table
	return table, nil
}

func (mapping *valueMapping) row(value introspect.ValueUsage, path string) mappingRow {
	row := mappingRow{path: path, defaultValue: value.DefaultValue, value: value, status: "other"}
	if !pathWithin(path, mapping.target) {
		return row
	}
	inputPath := transformPackagePath(path, mapping.target, mapping.source)
	row.packagePath = "." + inputPath
	row.input, row.present = packageValueInput(mapping.pkg.Values, inputPath)
	row.status = "available"
	if excluded, found := mapping.exclusion(inputPath); found {
		row.status, row.exclusion = "excluded", excluded
	}
	if mapping.usage.Runtime {
		row.status = "runtime"
	}
	// A retained known snapshot is useful evidence, but an unresolved layer can
	// alter it. Do not offer an authoring snippet based on a fabricated winner.
	if row.status == "available" && (unresolvedStatus(mapping.usage.Status) ||
		packageSourcesUnresolved(mapping.pkg.Sources)) {
		row.status = "unresolved"
	}
	if row.status == "available" {
		row.authoring = &valueAuthoring{path: row.packagePath, basis: authoringUnknown}
		if row.present {
			row.authoring.value, row.authoring.basis = row.input, authoringSupplied
		} else if value.DefaultValue != nil {
			row.authoring.value, row.authoring.basis = value.DefaultValue, authoringDefault
		}
	}
	return row
}

func (policy *packageValuesPolicy) details(chartKey string, value introspect.ValueUsage) []valueMappingDetail {
	if policy == nil {
		return nil
	}
	mappings := policy.byChart[chartKey]
	if len(mappings) == 0 {
		return nil
	}
	path := packagePath(value.Path)
	details := make([]valueMappingDetail, 0, len(mappings))
	for _, mapping := range mappings {
		details = append(details, valueMappingDetail{pkg: mapping.pkg, row: mapping.row(value, path), mapping: mapping})
	}
	return details
}

func packagePath(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "."), ".")
}

func pathWithin(path, parent string) bool {
	return parent == "" || path == parent || strings.HasPrefix(path, parent+".")
}

func packageLeaves(prefix string, values map[string]interface{}, visit func(string, interface{})) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := values[key].(map[string]interface{}); ok && len(nested) > 0 {
			packageLeaves(path, nested, visit)
		} else {
			visit(path, values[key])
		}
	}
}

// packageValueInput preserves the distinction between absent input and explicit
// null, false, zero, an empty string, or an empty object.
func packageValueInput(values map[string]interface{}, path string) (interface{}, bool) {
	if path == "" {
		return values, values != nil
	}
	var current interface{} = values
	for {
		segment, remainder, more := strings.Cut(path, ".")
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
		if !more {
			return current, true
		}
		path = remainder
	}
}
