package tui

import (
	"sort"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// packageValueInventory adds explicit schema properties to configured leaves.
// The inspection's values and schema remain borrowed, immutable snapshots;
// default annotations never become supplied input or authoring values.
func packageValueInventory(pkg *introspect.PackageValues) []packageValueLeaf {
	byPath := make(map[string]packageValueLeaf)
	packageLeaves("", pkg.Values, func(path string, value interface{}) {
		byPath[path] = packageValueLeaf{path: path, value: value, present: true}
	})
	for _, property := range introspect.SchemaInventory(pkg.Schema) {
		leaf := byPath[property.Path]
		leaf.path = property.Path
		leaf.value, leaf.present = packageValueInput(pkg.Values, property.Path)
		leaf.declared, leaf.schema, leaf.schemaRequired = true, property.Schema, property.Required
		byPath[property.Path] = leaf
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	leaves := make([]packageValueLeaf, 0, len(paths))
	for _, path := range paths {
		leaves = append(leaves, byPath[path])
	}
	return leaves
}

// bundleVariableItems preserves configured top-level variables while exposing
// every concrete variable path observed in a package's bundle-values sources.
// Missing references remain absent, including when their parent is configured.
func (m Model) bundleVariableItems() []DisplayItem {
	if m.bundle == nil {
		return nil
	}
	byPath := make(map[string]DisplayItem, len(m.bundle.Variables))
	for path, value := range m.bundle.Variables {
		byPath[path] = DisplayItem{
			ValueUsage: introspect.ValueUsage{Path: path, DefaultValue: value, DefaultPresent: true},
			Component:  "UDS Next bundle", IsBundleVariable: true, ValuePresent: true,
		}
	}
	for _, pkg := range m.report.PackageValues {
		for _, source := range pkg.Sources {
			for _, ref := range source.References {
				if ref.Scope != "bundle-vars" {
					continue
				}
				path := packagePath(ref.Path)
				if path == "" {
					continue
				}
				item, found := byPath[path]
				if !found {
					value, present := packageValueInput(m.bundle.Variables, path)
					item = DisplayItem{
						ValueUsage: introspect.ValueUsage{Path: path, DefaultValue: value, DefaultPresent: present},
						Component:  "UDS Next bundle", IsBundleVariable: true, ValuePresent: present,
					}
				}
				item.Locations = append(item.Locations, ref.Locations...)
				byPath[path] = item
			}
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	items := make([]DisplayItem, 0, len(paths))
	for _, path := range paths {
		item := byPath[path]
		item.Locations = uniqueInventoryLocations(item.Locations)
		items = append(items, item)
	}
	return items
}

// Locations are newly allocated by bundleVariableItems, so sorting and
// compacting them cannot alter the source report's reference snapshots.
func uniqueInventoryLocations(locations []introspect.ValueUsageLocation) []introspect.ValueUsageLocation {
	sort.Slice(locations, func(i, j int) bool {
		a, b := locations[i], locations[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Kind < b.Kind
	})
	n := 0
	for _, location := range locations {
		if n == 0 || locations[n-1] != location {
			locations[n] = location
			n++
		}
	}
	return locations[:n]
}
