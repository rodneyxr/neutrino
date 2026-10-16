package tui

import (
	"sort"
	"strings"
)

// settingsLayer identifies what a row represents separately from its owner,
// mapping status, or value. Categories retain the existing Next 1–7 shortcuts.
type settingsLayer struct {
	key, label      string
	order, category int
}

func (m Model) settingLayer(item DisplayItem) settingsLayer {
	layer := func(key, label string, category, position int) settingsLayer {
		return settingsLayer{key: key, label: label, category: category, order: category*100 + position}
	}
	switch {
	case item.IsBundleVariable:
		return layer("bundle-variables", "Bundle variables", 0, 0)
	case item.IsPackageValue && item.Component == "UDS Next bundle":
		return layer("bundle-values-files", "Bundle values files", 1, 0)
	case item.IsPkgVar || item.IsZarfVar:
		return layer("legacy-zarf-variables", "Legacy Zarf variables", 2, 0)
	case item.IsPackageMapping:
		if !runtimeReference(item) {
			return layer("package-mappings", "Zarf → Helm mappings", 4, 0)
		}
		if item.mappingRef != nil {
			for _, location := range item.mappingRef.usage.Locations {
				if location.Kind == "setValues" {
					return layer("runtime-assignments", "Zarf runtime assignments", 4, 30)
				}
			}
		}
		if referenceRequiresDeployment(item) {
			return layer("runtime-references", "Zarf runtime references", 4, 20)
		}
		return layer("template-references", "Zarf template references", 4, 10)
	case item.IsPackageValue:
		if !item.ValuePresent {
			if item.Schema != nil {
				return layer("package-schema", "Zarf schema · no input", 3, 60)
			}
			if item.Path == "(package values)" {
				return layer("package-files-schema", "Zarf values · files/schema", 3, 80)
			}
			return layer("package-unsupplied", "Zarf values · no input", 3, 70)
		}
		origin, pending := m.settingPackageOrigin(item)
		position := map[string]int{"packaged": 0, "bundle": 10, "operator": 20, "mixed": 30, "unknown": 40}[origin]
		label := "Zarf values · " + origin
		key := "package-" + origin
		if pending {
			// A surviving known contribution is not a claim that a skipped
			// template was applied, or that later deployment input is known.
			key += "-known"
			label += " · known input"
			position++
		}
		return layer(key, label, 3, position)
	case item.IsUDSOverrideValue:
		return layer("legacy-fixed-overrides", "Legacy bundle overrides · fixed", 6, 0)
	case item.IsUDSOverrideVar:
		return layer("legacy-configurable-overrides", "Legacy bundle overrides · configurable", 6, 10)
	default:
		return layer("helm-inputs", "Helm inputs", 5, 0)
	}
}

// settingPackageOrigin uses the surviving source of the inspected path. Equal
// overlays still own their value; Changed is deliberately unrelated. Object
// inputs may retain contributions from more than one source kind.
func (m Model) settingPackageOrigin(item DisplayItem) (string, bool) {
	pkg := m.tracePackage(item)
	if pkg == nil || len(pkg.Sources) == 0 {
		return "unknown", false
	}
	trace := m.cachedValueTrace(item, pkg.Sources, item.Path)
	if !trace.Present {
		// Serialized reports omit source snapshots. File order alone cannot
		// recover a per-path winner, even when the merged value is present.
		return "unknown", trace.Unresolved
	}
	kinds := make(map[string]bool)
	for _, contribution := range trace.History {
		if !contribution.Winner {
			continue
		}
		origin := ""
		switch contribution.Kind {
		case "packaged-default":
			origin = "packaged"
		case "bundle-values":
			origin = "bundle"
		case "operator-file":
			origin = "operator"
		default:
			return "unknown", trace.Unresolved
		}
		kinds[origin] = true
	}
	if len(kinds) > 1 {
		return "mixed", trace.Unresolved
	}
	for origin := range kinds {
		return origin, trace.Unresolved
	}
	return "unknown", trace.Unresolved
}

// settingOwner is a stable identity and a display label, not a source-origin
// inference. Chart destinations remain explicit on legacy override groups.
func (m Model) settingOwner(item DisplayItem) (string, string) {
	if item.IsBundleVariable {
		return "bundle", ""
	}
	if item.IsPackageValue && item.Component == "UDS Next bundle" {
		// Each file row already carries its package ID; one band makes the
		// bundle's ordered package inputs readable without redundant headings.
		return "bundle-files", ""
	}
	if item.IsUDSOverrideValue || item.IsUDSOverrideVar {
		destination := item.Component
		if item.IsSummaryItem && item.SummaryComponent != "" {
			destination = item.SummaryComponent
		}
		return "chart:" + destination, destination
	}
	if item.IsPackageValue || item.IsPackageMapping || item.IsPkgVar || item.IsZarfVar {
		if owner := m.scopeItemOwner(item); owner != "" {
			return "package:" + owner, owner
		}
		if item.IsPkgVar || item.IsPackageValue || item.IsPackageMapping {
			return "package:unknown", ""
		}
	}
	return "chart:" + item.Component, item.Component
}

func (m Model) settingGroupKey(item DisplayItem) string {
	owner, _ := m.settingOwner(item)
	return m.settingLayer(item).key + "\x00" + owner
}

func (m Model) settingGroupLabel(item DisplayItem) string {
	label := m.settingLayer(item).label
	_, owner := m.settingOwner(item)
	if owner != "" {
		label += " · " + owner
	}
	return label
}

// orderSettingsLayers returns a copy grouped by semantic layer and then owner.
// Owner order comes from first appearance in the established catalog; row order
// within a group is unchanged. This leaves borrowed report/catalog data intact.
func (m Model) orderSettingsLayers(items []DisplayItem) []DisplayItem {
	type rankedItem struct {
		item       DisplayItem
		layer      settingsLayer
		ownerOrder int
	}
	owners := make(map[string]int)
	ranked := make([]rankedItem, 0, len(items))
	for _, item := range items {
		owner, _ := m.settingOwner(item)
		order, exists := owners[owner]
		if !exists {
			order = len(owners)
			owners[owner] = order
		}
		ranked = append(ranked, rankedItem{item: item, layer: m.settingLayer(item), ownerOrder: order})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].layer.order != ranked[j].layer.order {
			return ranked[i].layer.order < ranked[j].layer.order
		}
		if ranked[i].layer.key != ranked[j].layer.key {
			return ranked[i].layer.key < ranked[j].layer.key
		}
		return ranked[i].ownerOrder < ranked[j].ownerOrder
	})
	result := make([]DisplayItem, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].item
	}
	return result
}

// settingLayerMatches shares the displayed layer vocabulary with kind queries,
// while retaining existing category words as aliases for saved filters.
func (m Model) settingLayerMatches(item DisplayItem, query string) bool {
	layer := m.settingLayer(item)
	text := layer.label + " " + layer.key + " " + nextCategories[layer.category]
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}
