package tui

import (
	"sort"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

type settingsHierarchyOwner struct {
	packageName string
	component   string
}

func settingsHierarchyPackage(item DisplayItem) bool {
	return item.IsPackageValue && !item.IsPackageMapping && item.Component != "UDS Next bundle"
}

func settingsHierarchyIdentity(item DisplayItem) settingsHierarchyOwner {
	return settingsHierarchyOwner{packageName: item.PackageName, component: item.Component}
}

// projectSettingsCatalog is a presentation-only projection of supplied package
// inputs. The policy inventory still includes schema-only paths for secondary
// mapping exploration. Paths, values, routes and source identities stay intact.
func (m Model) projectSettingsCatalog(items []DisplayItem) []DisplayItem {
	type packageRows struct {
		rows  []DisplayItem
		paths map[string]int
		pkg   *introspect.PackageValues
	}
	owners := make(map[settingsHierarchyOwner]*packageRows)
	retained := make([]DisplayItem, 0, len(items))
	for _, item := range items {
		if settingsHierarchyPackage(item) && !item.ValuePresent {
			continue
		}
		if settingsHierarchyPackage(item) {
			item.settingLabel, item.settingParent = item.Path, ""
			item.settingDepth, item.settingChildren, item.settingContext = 0, 0, false
			owner := settingsHierarchyIdentity(item)
			group := owners[owner]
			if group == nil {
				group = &packageRows{paths: make(map[string]int), pkg: m.tracePackage(item)}
				owners[owner] = group
			}
			if _, exists := group.paths[packagePath(item.Path)]; !exists {
				group.paths[packagePath(item.Path)] = len(group.rows)
			}
			group.rows = append(group.rows, item)
		}
		retained = append(retained, item)
	}
	for _, group := range owners {
		if group.pkg == nil {
			continue
		}
		// Only a real object in the inspected input can establish an ancestor.
		// In particular, a literal dotted key must not invent intermediate maps.
		originalCount := len(group.rows)
		for i := 0; i < originalCount; i++ {
			path := packagePath(group.rows[i].Path)
			if _, present := settingsHierarchyInput(group.pkg.Values, path); !present {
				continue
			}
			for cut := strings.LastIndex(path, "."); cut >= 0; cut = strings.LastIndex(path, ".") {
				path = path[:cut]
				value, present := packageValueInput(group.pkg.Values, path)
				object, ok := value.(map[string]interface{})
				if !present || !ok || len(object) == 0 {
					continue
				}
				if _, exists := group.paths[path]; exists {
					continue
				}
				entry := m.settingsObjectEntry(group.pkg, path, object)
				item := packageValueItem(entry, group.rows[i].Component)
				item.settingLabel = item.Path
				group.paths[path] = len(group.rows)
				group.rows = append(group.rows, item)
			}
		}
		for i := range group.rows {
			row := &group.rows[i]
			path := packagePath(row.Path)
			if path == "" {
				// The flattened path convention cannot address an empty key;
				// packageValueInput("") instead means the whole package object.
				continue
			}
			value, present := settingsHierarchyInput(group.pkg.Values, path)
			if !present {
				continue
			}
			if object, ok := value.(map[string]interface{}); ok && len(object) > 0 {
				row.settingChildren = len(object)
			}
			for cut := strings.LastIndex(path, "."); cut >= 0; cut = strings.LastIndex(path, ".") {
				path = path[:cut]
				if parent, exists := group.paths[path]; exists && group.rows[parent].ValuePresent {
					value, _ := packageValueInput(group.pkg.Values, path)
					if object, ok := value.(map[string]interface{}); ok && len(object) > 0 {
						row.settingParent = group.rows[parent].Path
						row.settingLabel = strings.TrimPrefix(packagePath(row.Path), path+".")
						break
					}
				}
			}
		}
	}
	// Emit each package as a preorder tree. Other settings retain their relative
	// order and are never made children merely because their path looks similar.
	emitted := make(map[settingsHierarchyOwner]bool)
	result := make([]DisplayItem, 0, len(retained))
	for _, item := range retained {
		if !settingsHierarchyPackage(item) {
			result = append(result, item)
			continue
		}
		owner := settingsHierarchyIdentity(item)
		if emitted[owner] {
			continue
		}
		emitted[owner] = true
		group := owners[owner]
		children := make(map[string][]int)
		for i, row := range group.rows {
			children[row.settingParent] = append(children[row.settingParent], i)
		}
		for parent := range children {
			sort.SliceStable(children[parent], func(i, j int) bool {
				return group.rows[children[parent][i]].Path < group.rows[children[parent][j]].Path
			})
		}
		var visit func(string, int)
		visit = func(parent string, depth int) {
			for _, i := range children[parent] {
				row := group.rows[i]
				row.settingDepth = depth
				result = append(result, row)
				// Duplicate source rows keep their identity, without duplicating
				// every descendant in the unlikely case of a repeated input path.
				if row.Path != "" && group.paths[packagePath(row.Path)] == i {
					visit(row.Path, depth+1)
				}
			}
		}
		visit("", 0)
	}
	return result
}

// Flattened catalog paths cannot distinguish a literal dotted key from nested
// object traversal. Keep ambiguous/unaddressable inputs at the root instead of
// inventing a parent from a matching prefix.
func settingsHierarchyInput(values map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var current interface{} = values
	for i, part := range parts {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		for end := i + 2; end <= len(parts); end++ {
			if _, literal := object[strings.Join(parts[i:end], ".")]; literal {
				return nil, false
			}
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// settingsObjectEntry derives metadata only from explicit declarations, exactly
// as the package policy does for supplied leaves. Names never imply a mapping.
func (m Model) settingsObjectEntry(pkg *introspect.PackageValues, path string, value map[string]interface{}) packageValueEntry {
	entry := packageValueEntry{pkg: pkg, path: path, value: value, present: true}
	if schema := schemaForPath(pkg.Schema, path); schema != nil {
		entry.schema = schema
	}
	policy := m.valuePolicy
	if policy == nil {
		policy = newPackageValuesPolicy(m.report)
	}
	seen := make(map[*valueMapping]bool)
	for _, candidate := range policy.catalog().packages {
		if candidate.pkg == nil || candidate.pkg.PackageName != pkg.PackageName {
			continue
		}
		mappings := []*valueMapping{candidate.mapping}
		for _, route := range candidate.routes {
			mappings = append(mappings, route.mapping)
		}
		for _, mapping := range mappings {
			if mapping == nil || seen[mapping] {
				continue
			}
			seen[mapping] = true
			if route, applies := mapping.route(path); applies {
				entry.routes = append(entry.routes, route)
			}
		}
	}
	entry.status = aggregatePackageRoutes(entry.routes)
	if entry.status != "excluded" && entry.status != "runtime" && entry.status != "mixed" && !packageSourcesUnresolved(pkg.Sources) {
		entry.authoring = &valueAuthoring{path: path, value: value, basis: authoringSupplied}
	}
	return entry
}

// filterSettingsCatalog retains only matching rows and their actual package
// ancestors. Context rows do not broaden the query to siblings or other owners.
func (m Model) filterSettingsCatalog(items []DisplayItem) []DisplayItem {
	type rowIdentity struct {
		owner settingsHierarchyOwner
		path  string
	}
	byPath := make(map[rowIdentity]int)
	matched, included := make([]bool, len(items)), make([]bool, len(items))
	for i, item := range items {
		if settingsHierarchyPackage(item) {
			key := rowIdentity{settingsHierarchyIdentity(item), item.Path}
			if _, exists := byPath[key]; !exists {
				byPath[key] = i
			}
		}
		matched[i] = m.itemInScope(item) && m.matchesWorkspaceFilter(item)
		included[i] = matched[i]
	}
	for i, item := range items {
		if !matched[i] || !settingsHierarchyPackage(item) {
			continue
		}
		for parent := item.settingParent; parent != ""; {
			index, exists := byPath[rowIdentity{settingsHierarchyIdentity(item), parent}]
			if !exists || !m.itemInScope(items[index]) || included[index] {
				break
			}
			included[index] = true
			parent = items[index].settingParent
		}
	}
	result := make([]DisplayItem, 0, len(items))
	for i, item := range items {
		if included[i] {
			item.settingContext = !matched[i]
			result = append(result, item)
		}
	}
	return result
}
