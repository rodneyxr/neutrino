package tui

import (
	"reflect"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func settingsHierarchyFixture(packages ...introspect.PackageValues) (Model, []DisplayItem) {
	report := introspect.InspectionReport{PackageValues: packages}
	model := Model{report: report, valuePolicy: newPackageValuesPolicy(report), currentChart: "All Charts"}
	var rows []DisplayItem
	for _, entry := range model.valuePolicy.catalog().packages {
		rows = append(rows, packageValueItem(entry, "Zarf Package Values"))
	}
	return model, rows
}

func hierarchyRow(t *testing.T, rows []DisplayItem, owner, path string) DisplayItem {
	t.Helper()
	for _, row := range rows {
		if settingsHierarchyPackage(row) && row.PackageName == owner && row.Path == path {
			return row
		}
	}
	t.Fatalf("missing package input %s/%s", owner, path)
	return DisplayItem{}
}

func TestSettingsHierarchyEnvoyKeepsActualMappingBoundary(t *testing.T) {
	// These are the audited Legacy/Next shapes: the Legacy package exposes two
	// mappings, while Next's absent optional chart archives provide none.
	for _, name := range []string{"legacy", "next"} {
		t.Run(name, func(t *testing.T) {
			pkg := introspect.PackageValues{PackageName: "core-base", Values: map[string]interface{}{
				"envoy-gateway": map[string]interface{}{
					"envoy-gateway": map[string]interface{}{}, "uds-envoy-gateway-config": map[string]interface{}{},
				},
			}}
			if name == "legacy" {
				pkg.Usages = []introspect.PackageValueUsage{
					{Path: ".envoy-gateway.envoy-gateway", TargetPath: ".", ComponentName: "envoy-gateway", ChartName: "envoy-gateway"},
					{Path: ".envoy-gateway.uds-envoy-gateway-config", TargetPath: ".", ComponentName: "envoy-gateway", ChartName: "uds-envoy-gateway-config"},
				}
			} else {
				pkg.Sources = []introspect.ValueSource{{ID: "bundle", Kind: "bundle-values", Status: introspect.ValueDynamic}}
			}
			model, catalog := settingsHierarchyFixture(pkg)
			rows := model.projectSettingsCatalog(catalog)
			parent := hierarchyRow(t, rows, "core-base", "envoy-gateway")
			if parent.settingChildren != 2 || parent.settingDepth != 0 || parent.settingParent != "" || parent.settingLabel != "envoy-gateway" || len(parent.Routes) != 0 {
				t.Fatalf("incorrect structural parent: %+v", parent)
			}
			for _, child := range []string{"envoy-gateway", "uds-envoy-gateway-config"} {
				row := hierarchyRow(t, rows, "core-base", "envoy-gateway."+child)
				if row.settingLabel != child || row.settingParent != parent.Path || row.settingDepth != 1 || row.settingChildren != 0 || !row.ValuePresent {
					t.Fatalf("empty object must remain a real child input: %+v", row)
				}
				object, ok := row.DefaultValue.(map[string]interface{})
				if !ok || len(object) != 0 {
					t.Fatalf("empty object value lost: %#v", row.DefaultValue)
				}
				if name == "legacy" {
					if len(row.Routes) != 1 || row.MappingStatus != "mapped" || row.Routes[0].Usage.ChartName != child {
						t.Fatalf("explicit mapping lost: %+v", row)
					}
				} else if len(row.Routes) != 0 || row.MappingStatus != "unmapped" {
					t.Fatalf("chart-like names invented a Next mapping: %+v", row)
				}
			}
		})
	}
}

func TestSettingsHierarchyHidesSchemaOnlyWithoutChangingPolicy(t *testing.T) {
	pkg := introspect.PackageValues{PackageName: "app", Values: map[string]interface{}{"enabled": false}, Schema: map[string]interface{}{
		"properties": map[string]interface{}{"enabled": map[string]interface{}{"type": "boolean"}, "absent": map[string]interface{}{"type": "string", "default": "annotation"}},
	}, Usages: []introspect.PackageValueUsage{{Path: ".absent", TargetPath: ".destination", ComponentName: "server", ChartName: "app"},
		{Path: ".State.node", Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: "runtimeContext"}}}}}
	model, catalog := settingsHierarchyFixture(pkg, introspect.PackageValues{PackageName: "files-only", Files: []string{"values.yaml"}})
	policyBefore := append([]packageValueEntry(nil), model.valuePolicy.catalog().packages...)
	bundleFile := DisplayItem{IsPackageValue: true, Component: "UDS Next bundle", PackageName: "app", ValueFiles: []string{"bundle.yaml"}}
	helm := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "helm.schema", Schema: map[string]interface{}{"type": "string"}}, Component: "server/app"}
	catalog = append(catalog, bundleFile, helm)
	rows := model.projectSettingsCatalog(catalog)
	if len(rows) != 5 { // supplied false, explicit mapping, runtime reference, bundle file, Helm schema
		t.Fatalf("wrong surviving surfaces: %+v", rows)
	}
	for _, row := range rows {
		if settingsHierarchyPackage(row) && !row.ValuePresent {
			t.Fatalf("schema-only or files placeholder reached main settings: %+v", row)
		}
	}
	if !reflect.DeepEqual(model.valuePolicy.catalog().packages, policyBefore) {
		t.Fatal("projection changed policy inventory used for mapping exploration")
	}
	foundSchema := false
	for _, entry := range model.valuePolicy.catalog().packages {
		foundSchema = foundSchema || entry.path == "absent" && entry.schema != nil && !entry.present
	}
	if !foundSchema || !reflect.DeepEqual(rows[len(rows)-2], bundleFile) || !reflect.DeepEqual(rows[len(rows)-1], helm) {
		t.Fatal("secondary schema inventory or unrelated settings changed")
	}
}

func TestSettingsHierarchyPreservesTerminalInputs(t *testing.T) {
	values := map[string]interface{}{"": "empty-key", "false": false, "null": nil, "zero": 0, "empty-string": "", "empty-object": map[string]interface{}{}, "empty-list": []interface{}{}, "list": []interface{}{1, 2}}
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "app", Values: values})
	rows := model.projectSettingsCatalog(catalog)
	if len(rows) != len(values) {
		t.Fatalf("terminal inputs lost: %+v", rows)
	}
	for path, value := range values {
		row := hierarchyRow(t, rows, "app", path)
		if !row.ValuePresent || !row.DefaultPresent || !reflect.DeepEqual(row.DefaultValue, value) || row.settingChildren != 0 || row.MappingStatus != "unmapped" {
			t.Fatalf("terminal input %s changed: %+v", path, row)
		}
	}
}

func TestSettingsHierarchySynthesizesNestedObjectsWithoutSchema(t *testing.T) {
	values := map[string]interface{}{"app": map[string]interface{}{"database": map[string]interface{}{"host": "db.test", "port": 5432}, "enabled": true}, "app-sibling": 1}
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "api", Values: values, Files: []string{"defaults.yaml"}, FileSources: []string{"packaged-default"},
		Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "api"}}})
	original := append([]DisplayItem(nil), catalog...)
	rows := model.projectSettingsCatalog(catalog)
	wantPaths := []string{"app", "app.database", "app.database.host", "app.database.port", "app.enabled", "app-sibling"}
	var gotPaths []string
	for _, row := range rows {
		if !row.IsPackageMapping {
			gotPaths = append(gotPaths, row.Path)
		}
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) || !reflect.DeepEqual(catalog, original) {
		t.Fatalf("wrong preorder or input mutation: %v", gotPaths)
	}
	database := hierarchyRow(t, rows, "api", "app.database")
	if database.settingParent != "app" || database.settingLabel != "database" || database.settingDepth != 1 || database.settingChildren != 2 || database.MappingStatus != "mapped" || len(database.Routes) != 1 || database.Routes[0].Target != "config.database" {
		t.Fatalf("synthesized group metadata incorrect: %+v", database)
	}
	if !reflect.DeepEqual(database.ValueFiles, []string{"defaults.yaml"}) || !reflect.DeepEqual(database.ValueFileSources, []string{"packaged-default"}) || !reflect.DeepEqual(database.DefaultValue, values["app"].(map[string]interface{})["database"]) {
		t.Fatal("synthesized group lost supplied value/source identity")
	}
	if row := hierarchyRow(t, rows, "api", "app.database.host"); row.settingLabel != "host" || row.settingDepth != 2 || row.settingParent != database.Path {
		t.Fatalf("nested label is not relative to its visible parent: %+v", row)
	}
}

func TestSettingsHierarchyDottedKeysDoNotInventObjects(t *testing.T) {
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "app", Values: map[string]interface{}{
		"literal.key": map[string]interface{}{"child": true}, "another.literal": false,
	}})
	rows := model.projectSettingsCatalog(catalog)
	if len(rows) != 2 {
		t.Fatalf("dotted keys invented intermediate objects: %+v", rows)
	}
	for _, row := range rows {
		if row.settingParent != "" || row.settingDepth != 0 || row.settingChildren != 0 || row.settingLabel != row.Path {
			t.Fatalf("uncorroborated hierarchy for literal dotted key: %+v", row)
		}
	}
}

func TestSettingsHierarchyFilterKeepsOnlyMatchingOwnerAncestors(t *testing.T) {
	values := func(host string) map[string]interface{} {
		return map[string]interface{}{"app": map[string]interface{}{"database": map[string]interface{}{"host": host, "port": 5432}, "enabled": true}}
	}
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "api", Values: values("api.test")}, introspect.PackageValues{PackageName: "api.extra", Values: values("extra.test")})
	projected := model.projectSettingsCatalog(catalog)
	original := append([]DisplayItem(nil), projected...)
	model.currentChart = "package:api"
	model.filterInput.SetValue("host")
	filtered := model.filterSettingsCatalog(projected)
	if len(filtered) != 3 {
		t.Fatalf("query broadened to siblings or lost ancestors: %+v", filtered)
	}
	for i, path := range []string{"app", "app.database", "app.database.host"} {
		if filtered[i].PackageName != "api" || filtered[i].Path != path || filtered[i].settingContext != (i < 2) {
			t.Fatalf("wrong ancestor context: %+v", filtered[i])
		}
	}
	if !reflect.DeepEqual(projected, original) {
		t.Fatal("filter mutated projected input")
	}
	model.filterInput.SetValue("app.database")
	filtered = model.filterSettingsCatalog(projected)
	if len(filtered) != 4 || !filtered[0].settingContext || filtered[1].settingContext || filtered[2].settingContext || filtered[3].settingContext {
		t.Fatalf("directly matched ancestors mislabeled as context: %+v", filtered)
	}
}

func TestSettingsHierarchyStaysTogetherAcrossSourceLayers(t *testing.T) {
	packaged := map[string]interface{}{"app": map[string]interface{}{"enabled": true}}
	operator := map[string]interface{}{"app": map[string]interface{}{"replicas": 3}}
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "api", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": true, "replicas": 3}}, Sources: []introspect.ValueSource{
		{ID: "defaults", Kind: "packaged-default", Status: introspect.ValueResolved, Values: packaged},
		{ID: "operator", Kind: "operator-file", Status: introspect.ValueResolved, Values: operator},
	}})
	rows := model.projectSettingsCatalog(catalog)
	if len(rows) != 3 || rows[0].Path != "app" || rows[1].settingParent != "app" || rows[2].settingParent != "app" {
		t.Fatalf("source layers split the package hierarchy: %+v", rows)
	}
	if model.settingLayer(rows[1]).key != "package-packaged" || model.settingLayer(rows[2]).key != "package-operator" || model.settingLayer(rows[0]).key != "package-mixed" {
		t.Fatal("hierarchy changed the source provenance of its rows")
	}
	model.filterInput.SetValue("replicas")
	filtered := model.filterSettingsCatalog(rows)
	if len(filtered) != 2 || filtered[0].Path != "app" || !filtered[0].settingContext || filtered[1].Path != "app.replicas" || filtered[1].settingParent != "app" || filtered[1].settingContext {
		t.Fatalf("source layers prevented ancestor query context: %+v", filtered)
	}
}

func TestSettingsHierarchyDottedLiteralBesideRealObjectStaysAtRoot(t *testing.T) {
	for _, values := range []map[string]interface{}{
		{"app": map[string]interface{}{"real": 1}, "app.literal": 2},
		{"app": map[string]interface{}{"real": 1, "literal": 2}, "app.literal": 2},
	} {
		model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "api", Values: values})
		rows := model.projectSettingsCatalog(catalog)
		literal := hierarchyRow(t, rows, "api", "app.literal")
		if literal.settingParent != "" || literal.settingDepth != 0 || literal.settingLabel != "app.literal" {
			t.Fatalf("dotted key acquired a false parent: %+v", literal)
		}
		real := hierarchyRow(t, rows, "api", "app.real")
		if real.settingParent != "app" || real.settingDepth != 1 {
			t.Fatalf("unambiguous nested child lost its actual parent: %+v", real)
		}
	}
}

func TestSettingsHierarchyRejectsIntermediateDottedPrefix(t *testing.T) {
	values := map[string]interface{}{
		"a":   map[string]interface{}{"b": map[string]interface{}{"c": 2}},
		"a.b": map[string]interface{}{"c": 1},
	}
	if _, ok := settingsHierarchyInput(values, "a.b.c"); ok {
		t.Fatal("intermediate dotted prefix is ambiguous even with a traversable nested path")
	}
	model, catalog := settingsHierarchyFixture(introspect.PackageValues{PackageName: "api", Values: values})
	row := hierarchyRow(t, model.projectSettingsCatalog(catalog), "api", "a.b.c")
	if row.settingParent != "" || row.settingDepth != 0 {
		t.Fatalf("ambiguous input acquired an unrelated parent: %+v", row)
	}
}
