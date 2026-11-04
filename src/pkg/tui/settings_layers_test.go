package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func settingLayerFixture(sources []introspect.ValueSource, path string, input interface{}) (Model, DisplayItem) {
	model := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{
		PackageName: "app", Sources: sources,
	}}})
	item := DisplayItem{ValueUsage: introspect.ValueUsage{Path: path, DefaultValue: input},
		Component: "Zarf Package Values", PackageName: "app", IsPackageValue: true, ValuePresent: true}
	return model, item
}

func TestSettingLayerUsesSourceWinnersNotChanged(t *testing.T) {
	source := func(id, kind string, values map[string]interface{}) introspect.ValueSource {
		return introspect.ValueSource{ID: id, Kind: kind, Status: introspect.ValueResolved, Values: values}
	}
	packaged := source("defaults", "packaged-default", map[string]interface{}{"enabled": false})
	bundle := source("bundle", "bundle-values", map[string]interface{}{"enabled": false})
	operator := source("operator", "operator-file", map[string]interface{}{"enabled": false})
	for _, test := range []struct {
		name, origin string
		sources      []introspect.ValueSource
	}{
		{"packaged", "packaged", []introspect.ValueSource{packaged}},
		{"equal bundle overlay", "bundle", []introspect.ValueSource{packaged, bundle}},
		{"equal operator overlay", "operator", []introspect.ValueSource{packaged, bundle, operator}},
		{"unrelated later source", "bundle", []introspect.ValueSource{packaged, bundle, source("unrelated", "operator-file", map[string]interface{}{"other": 1})}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, item := settingLayerFixture(test.sources, "enabled", false)
			got := model.settingLayer(item)
			if got.key != "package-"+test.origin || got.label != "Zarf values · "+test.origin || got.category != 3 {
				t.Fatalf("wrong source layer: %+v", got)
			}
			if trace, ok := model.valueTrace(item); !ok || trace.Changed {
				t.Fatal("equal overlays must remain unchanged while their source owns the input")
			}
		})
	}
}

func TestSettingLayerObjectsUnknownSnapshotsAndPendingInputs(t *testing.T) {
	first := introspect.ValueSource{ID: "first", Kind: "packaged-default", Status: introspect.ValueResolved,
		Values: map[string]interface{}{"app": map[string]interface{}{"host": "example.test", "port": 80}}}
	second := introspect.ValueSource{ID: "second", Kind: "bundle-values", Status: introspect.ValueResolved,
		Values: map[string]interface{}{"app": map[string]interface{}{"port": 90}}}
	model, item := settingLayerFixture([]introspect.ValueSource{first, second}, "app", map[string]interface{}{"host": "example.test", "port": 90})
	if got := model.settingLayer(item); got.key != "package-mixed" {
		t.Fatalf("surviving object contributors from distinct kinds must be mixed: %+v", got)
	}
	second.Kind = "packaged-default"
	model, item = settingLayerFixture([]introspect.ValueSource{first, second}, "app", item.DefaultValue)
	if got := model.settingLayer(item); got.key != "package-packaged" {
		t.Fatalf("several packaged files are one source kind, not mixed: %+v", got)
	}
	unknown := introspect.ValueSource{ID: "pending", Kind: "bundle-values", Status: introspect.ValueDynamic}
	model, item = settingLayerFixture([]introspect.ValueSource{first, unknown}, "app.port", 80)
	if got := model.settingLayer(item); got.key != "package-packaged-known" || !strings.HasSuffix(got.label, "known input") {
		t.Fatalf("an unresolved bundle file cannot become the supplied origin: %+v", got)
	}
	first.Values = nil // As in JSON: source metadata remains, snapshots are omitted.
	model, item = settingLayerFixture([]introspect.ValueSource{first}, "app.port", 80)
	if got := model.settingLayer(item); got.key != "package-unknown" {
		t.Fatalf("source filenames cannot establish a serialized report's winner: %+v", got)
	}
	model, item = settingLayerFixture(nil, "nullable", nil)
	if got := model.settingLayer(item); got.key != "package-unknown" {
		t.Fatalf("present null is not a missing or schema-only input: %+v", got)
	}
	model, item = settingLayerFixture([]introspect.ValueSource{{ID: "null", Kind: "packaged-default", Status: introspect.ValueResolved,
		Values: map[string]interface{}{"nullable": nil}}}, "nullable", nil)
	if got := model.settingLayer(item); got.key != "package-packaged" {
		t.Fatalf("a supplied null retains its packaged source: %+v", got)
	}
	item.ValuePresent, item.Schema = false, map[string]interface{}{"type": "string"}
	if got := model.settingLayer(item); got.key != "package-schema" || got.label != "Zarf schema · no input" {
		t.Fatalf("schema-only property must not be labeled a packaged default: %+v", got)
	}
}

func TestSettingLayerSeparatesSurfacesAndPreservesCategories(t *testing.T) {
	model := Model{}
	runtime := &valueMapping{usage: introspect.PackageValueUsage{Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: "runtimeContext"}}}}
	assignment := &valueMapping{usage: introspect.PackageValueUsage{Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: "setValues"}}}}
	for _, test := range []struct {
		key      string
		category int
		item     DisplayItem
	}{
		{"bundle-variables", 0, DisplayItem{IsBundleVariable: true}},
		{"bundle-values-files", 1, DisplayItem{IsPackageValue: true, Component: "UDS Next bundle"}},
		{"legacy-zarf-variables", 2, DisplayItem{IsPkgVar: true}},
		{"legacy-zarf-variables", 2, DisplayItem{IsZarfVar: true}},
		{"package-mappings", 4, DisplayItem{IsPackageMapping: true, ValueTarget: ".destination"}},
		{"template-references", 4, DisplayItem{IsPackageMapping: true, Runtime: true}},
		{"runtime-references", 4, DisplayItem{ValueUsage: introspect.ValueUsage{Path: ".State.node"}, IsPackageMapping: true, Runtime: true, mappingRef: runtime}},
		{"runtime-assignments", 4, DisplayItem{IsPackageMapping: true, Runtime: true, mappingRef: assignment}},
		{"helm-inputs", 5, DisplayItem{}},
		{"legacy-fixed-overrides", 6, DisplayItem{IsUDSOverrideValue: true}},
		{"legacy-configurable-overrides", 6, DisplayItem{IsUDSOverrideVar: true}},
	} {
		got := model.settingLayer(test.item)
		if got.key != test.key || got.category != test.category || got.category != nextCategory(test.item) {
			t.Fatalf("surface %s classified incorrectly: %+v", test.key, got)
		}
	}
	input := DisplayItem{Component: "Zarf Package Values", PackageName: "app", IsPackageValue: true, ValuePresent: true}
	use := DisplayItem{Component: "Zarf Package Values", PackageName: "app", IsPackageMapping: true, Runtime: true}
	if model.settingGroupKey(input) == model.settingGroupKey(use) {
		t.Fatal("runtime observations must not inherit a structured-input group")
	}
	if label := model.settingGroupLabel(use); label != "app" {
		t.Fatalf("selected type already identifies the surface; band must identify owner: %s", label)
	}
	model.allCategories = true
	if label := model.settingGroupLabel(use); label != "Mappings & uses · app" {
		t.Fatalf("All must identify type and owner: %s", label)
	}
	if !model.settingLayerMatches(use, "template") || !model.settingLayerMatches(use, "mappings") {
		t.Fatal("kind matching must accept displayed layers and existing category aliases")
	}
}

func TestOrderSettingsLayersGroupsStablyWithoutMutatingRows(t *testing.T) {
	model := Model{}
	row := func(path, owner string) DisplayItem {
		return DisplayItem{ValueUsage: introspect.ValueUsage{Path: path}, PackageName: owner, IsPackageValue: true, ValuePresent: true}
	}
	items := []DisplayItem{
		row("b-first", "b"),
		{ValueUsage: introspect.ValueUsage{Path: "helm"}, Component: "chart"},
		row("a-first", "a"), row("b-second", "b"),
		{ValueUsage: introspect.ValueUsage{Path: "bundle"}, IsBundleVariable: true},
		{ValueUsage: introspect.ValueUsage{Path: "runtime"}, PackageName: "b", IsPackageMapping: true, Runtime: true},
		{ValueUsage: introspect.ValueUsage{Path: "fixed"}, Component: "chart", IsUDSOverrideValue: true},
	}
	original := append([]DisplayItem(nil), items...)
	ordered := model.orderSettingsLayers(items)
	var paths []string
	for _, item := range ordered {
		paths = append(paths, item.Path)
	}
	want := []string{"bundle", "b-first", "b-second", "a-first", "runtime", "helm", "fixed"}
	if !reflect.DeepEqual(paths, want) || !reflect.DeepEqual(items, original) {
		t.Fatalf("wrong stable grouping or input mutation: got %v, want %v", paths, want)
	}
}
