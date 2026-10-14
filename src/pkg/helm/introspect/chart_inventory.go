package introspect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	v2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	yamlreader "k8s.io/apimachinery/pkg/util/yaml"
)

// inventoryChart joins template references with all declared values and explicit
// schema properties. Effective values are only inspected file merges; schema
// defaults and deploy-time mappings never enter this tree.
func inventoryChart(ctx context.Context, c *v2.Chart, result *IntrospectResult, opts IntrospectOptions, filePrefix, valuePrefix string, effective interface{}) {
	indices := make(map[string]int)
	for i, usage := range result.Values {
		if usage.Chart == c.Metadata.Name && (valuePrefix == "" || strings.HasPrefix(usage.Path, valuePrefix+".")) {
			indices[strings.TrimPrefix(usage.Path, valuePrefix+".")] = i
		}
	}
	ensure := func(path string) int {
		if i, ok := indices[path]; ok {
			return i
		}
		i := len(result.Values)
		result.Values = append(result.Values, ValueUsage{
			Path:      joinValuePath(valuePrefix, path),
			Chart:     c.Metadata.Name,
			Locations: []ValueUsageLocation{},
		})
		indices[path] = i
		return i
	}

	paths := make(map[string]bool)
	addPaths := func(values interface{}) {
		for _, path := range FlattenToPaths("", values) {
			// This known chart wrapper supplies defaults for the public paths
			// templates use. Inventory those paths without duplicating the
			// private storage tree; explicit template references stay untouched.
			if path == "_internal_defaults_do_not_set" {
				continue
			}
			path = strings.TrimPrefix(path, "_internal_defaults_do_not_set.")
			paths[path] = true
		}
	}
	addPaths(c.Values)
	addPaths(effective)
	var locations map[string]ValueUsageLocation
	for _, file := range c.Raw {
		if file.Name != "values.yaml" {
			continue
		}
		name := filepath.Join(filePrefix, file.Name)
		newFile := !slices.Contains(result.ValuesFiles, name)
		if newFile {
			result.ValuesFiles = append(result.ValuesFiles, name)
		}
		var err error
		locations, err = ParseValuesLocations(file.Data)
		if err != nil {
			logger.From(ctx).Warn(fmt.Sprintf("Failed to parse %s for locations: %s", name, err))
		}
		// Union declarations from every YAML document before any file or later
		// document can replace an ancestor. Use Helm's document reader, while
		// retaining the paths separately from the effective merged values.
		reader := yamlreader.NewYAMLReader(bufio.NewReader(bytes.NewReader(file.Data)))
		for {
			raw, err := reader.Read()
			if err != nil {
				break
			}
			var declared map[string]interface{}
			if yaml.Unmarshal(raw, &declared) == nil {
				addPaths(declared)
			}
		}
		if newFile {
			for variable, locs := range FindZarfVariables(file.Data, name) {
				mergeZarfVars(result, variable, locs)
			}
		}
		break
	}
	if !opts.NoValues {
		ordered := make([]string, 0, len(paths))
		for path := range paths {
			ordered = append(ordered, path)
		}
		sort.Strings(ordered)
		for _, path := range ordered {
			ensure(path)
		}
	}

	if len(c.Schema) > 0 {
		entry := ChartValuesSchema{
			Chart: c.Metadata.Name, ValuePrefix: valuePrefix,
			File: filepath.Join(filePrefix, "values.schema.json"),
		}
		if err := json.Unmarshal(c.Schema, &entry.Schema); err != nil {
			// Preserve the existence of a malformed schema without blocking the
			// chart's other static input evidence or exposing its raw contents.
			entry.Diagnostic = "Could not parse values.schema.json as JSON."
		} else {
			switch entry.Schema.(type) {
			case map[string]interface{}, bool:
			default:
				entry.Diagnostic = "values.schema.json is not an object or boolean schema."
			}
			for _, property := range SchemaInventory(entry.Schema) {
				i, exists := indices[property.Path]
				if !exists {
					if opts.NoValues {
						continue
					}
					i = ensure(property.Path)
				}
				result.Values[i].Schema = property.Schema
				result.Values[i].SchemaRequired = property.Required
				result.Values[i].Locations = append(result.Values[i].Locations, ValueUsageLocation{File: entry.File, Kind: "schema"})
			}
		}
		result.ValuesSchemas = append(result.ValuesSchemas, entry)
	}

	for path, i := range indices {
		value, present, blocked := lookupInventoryValue(effective, path)
		if !present && !blocked {
			// Retain the existing internal-default wrapper convention, while
			// respecting explicit null or scalar replacement at an ancestor.
			value, present, _ = lookupInventoryValue(effective, "_internal_defaults_do_not_set."+path)
		}
		result.Values[i].DefaultValue = value
		result.Values[i].DefaultPresent = present
		location, hasLocation := locations[path]
		if !hasLocation {
			location, hasLocation = locations["_internal_defaults_do_not_set."+path]
		}
		if hasLocation {
			location.File = filepath.Join(filePrefix, location.File)
			result.Values[i].Locations = append(result.Values[i].Locations, location)
		}
	}

	if opts.IncludeSubcharts {
		for _, dep := range chartDependencies(c) {
			var childValues interface{} = dep.chart.Values
			override, present, blocked := lookupInventoryValue(effective, dep.valueKey)
			if present || blocked {
				if patch, ok := override.(map[string]interface{}); ok && !blocked {
					childValues = mergeMaps(cloneTraceValue(dep.chart.Values).(map[string]interface{}), patch)
				} else {
					childValues = override
				}
			}
			inventoryChart(ctx, dep.chart, result, opts,
				filepath.Join(filePrefix, "charts", dep.chart.Metadata.Name),
				joinValuePath(valuePrefix, dep.valueKey), childValues)
		}
	}
}

// lookupInventoryValue preserves null and distinguishes an absent map entry
// from a descendant removed by an explicit scalar/list/null ancestor.
func lookupInventoryValue(values interface{}, path string) (value interface{}, present, blocked bool) {
	current := values
	for _, part := range strings.Split(path, ".") {
		mapping, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, true
		}
		current, ok = mapping[part]
		if !ok {
			return nil, false, false
		}
	}
	return current, true, false
}

type chartDependency struct {
	chart    *v2.Chart
	valueKey string
}

// Each declared alias owns a distinct values namespace and selects the first
// packaged chart compatible with its version requirement, as Helm does. Aliases
// may share that chart; source files still belong to the selected chart.
func chartDependencies(parent *v2.Chart) []chartDependency {
	var dependencies []chartDependency
	keys := make(map[string]bool)
	for _, child := range parent.Dependencies() {
		matched := false
		for _, declaration := range parent.Metadata.Dependencies {
			if declaration.Name != child.Metadata.Name || !chartutil.IsCompatibleRange(declaration.Version, child.Metadata.Version) {
				continue
			}
			matched = true
			key := declaration.Name
			if declaration.Alias != "" {
				key = declaration.Alias
			}
			if !keys[key] {
				dependencies = append(dependencies, chartDependency{chart: child, valueKey: key})
				keys[key] = true
			}
		}
		if !matched {
			dependencies = append(dependencies, chartDependency{chart: child, valueKey: child.Metadata.Name})
		}
	}
	return dependencies
}
