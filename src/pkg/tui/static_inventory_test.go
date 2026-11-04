package tui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func TestStaticPackageInventoryUnionsDeclaredAndConfiguredPaths(t *testing.T) {
	declared := map[string]interface{}{"type": "string", "default": "annotation only"}
	report := introspect.InspectionReport{PackageValues: []introspect.PackageValues{{
		PackageName: "core",
		Values: map[string]interface{}{
			"app":   map[string]interface{}{"enabled": false, "extra": 0, "nullable": nil},
			"plain": "configured", "scalar": "configured scalar",
		},
		Schema: map[string]interface{}{
			"required": []interface{}{"app"},
			"properties": map[string]interface{}{
				"app": map[string]interface{}{
					"type": "object", "required": []interface{}{"missing"},
					"properties": map[string]interface{}{
						"enabled":  map[string]interface{}{"type": "boolean", "default": true},
						"missing":  declared,
						"nullable": map[string]interface{}{"default": "must not replace null"},
					},
				},
				"allowed": true, "forbidden": false,
				"array":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"properties": map[string]interface{}{"inferred": true}}},
				"ref":    map[string]interface{}{"$ref": "#/$defs/object"},
				"scalar": map[string]interface{}{"properties": map[string]interface{}{"child": declared}},
			},
			"$defs": map[string]interface{}{"object": map[string]interface{}{"properties": map[string]interface{}{"inferred": true}}},
			"allOf": []interface{}{map[string]interface{}{"properties": map[string]interface{}{"inferred": true}}},
		},
	}}}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	policy := newPackageValuesPolicy(report)
	var paths []string
	for _, entry := range policy.catalog().packages {
		paths = append(paths, entry.path)
	}
	wantPaths := []string{"allowed", "app", "app.enabled", "app.extra", "app.missing", "app.nullable", "array", "forbidden", "plain", "ref", "scalar", "scalar.child"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("inventory paths = %v, want %v", paths, wantPaths)
	}
	for _, path := range []string{"app", "app.enabled", "app.extra", "app.nullable", "plain", "scalar"} {
		entry := policyEntry(t, policy.catalog(), "core", path)
		if !entry.present || entry.authoring == nil || entry.authoring.basis != authoringSupplied {
			t.Fatalf("configured value lost at %s: %+v", path, entry)
		}
	}
	for _, path := range []string{"allowed", "app.missing", "array", "forbidden", "ref", "scalar.child"} {
		entry := policyEntry(t, policy.catalog(), "core", path)
		if entry.present || entry.value != nil || entry.authoring != nil {
			t.Fatalf("schema-only path became supplied input at %s: %+v", path, entry)
		}
	}
	if entry := policyEntry(t, policy.catalog(), "core", "app"); !entry.schemaRequired {
		t.Fatal("parent's required annotation was lost")
	}
	if entry := policyEntry(t, policy.catalog(), "core", "app.missing"); !entry.schemaRequired || !reflect.DeepEqual(entry.schema, declared) {
		t.Fatalf("exact property schema/required annotation lost: %+v", entry)
	}
	if entry := policyEntry(t, policy.catalog(), "core", "app.enabled"); entry.value != false || entry.schemaRequired {
		t.Fatalf("false input or immediate required membership changed: %+v", entry)
	}
	if entry := policyEntry(t, policy.catalog(), "core", "app.nullable"); entry.value != nil || entry.authoring.value != nil {
		t.Fatalf("explicit null replaced by schema default: %+v", entry)
	}
	if entry := policyEntry(t, policy.catalog(), "core", "forbidden"); entry.schema != false {
		t.Fatalf("boolean schema lost: %+v", entry)
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("inventory mutated the inspection snapshot")
	}
}

func TestStaticPackageInventoryKeepsAbsentMappedPaths(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web", ExcludePaths: []string{".app.excluded"}}
	exact := introspect.PackageValueUsage{Path: ".app.missing", TargetPath: ".mapped", ComponentName: "server", ChartName: "web"}
	report := introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"server/web": {Values: []introspect.ValueUsage{{Path: "config.discovered", DefaultValue: false}}}},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{}, Usages: []introspect.PackageValueUsage{usage, exact},
			Schema: map[string]interface{}{"properties": map[string]interface{}{"app": map[string]interface{}{
				"type": "object", "properties": map[string]interface{}{
					"missing": map[string]interface{}{"default": 99}, "excluded": map[string]interface{}{"default": false},
				},
			}}},
		}},
	}
	policy := newPackageValuesPolicy(report)
	entry := policyEntry(t, policy.catalog(), "core", "app.missing")
	if entry.present || len(entry.routes) != 2 || entry.routes[0].Target != "config.missing" || entry.routes[1].Target != "mapped" || entry.authoring != nil {
		t.Fatalf("absent declared value lost mapping routes or became supplied: %+v", entry)
	}
	refs := policyDeclarations(policy.catalog(), "core")
	for i, path := range []string{"config.missing", "mapped"} {
		table, err := policy.open(refs[i])
		if err != nil {
			t.Fatal(err)
		}
		row := policyRow(t, table, path)
		if table.sourcePresent || row.present || row.input != nil || !row.undiscovered || row.packagePath != ".app.missing" {
			t.Fatalf("absent path omitted or fabricated input: %+v", row)
		}
		if row.defaultValue != nil || row.value.DefaultValue != nil || row.value.Schema != nil || row.authoring == nil || row.authoring.basis != authoringUnknown || row.authoring.value != nil {
			t.Fatalf("package schema annotation became chart metadata or authoring: %+v", row)
		}
		counts := make(map[string]int)
		for _, row := range table.rows {
			counts[row.path]++
			if counts[row.path] > 1 {
				t.Fatalf("duplicate mapping path %s", row.path)
			}
		}
	}
	table, err := policy.open(refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if row := policyRow(t, table, "config.excluded"); row.status != "excluded" || row.authoring != nil || row.present {
		t.Fatalf("absent excluded row changed semantics: %+v", row)
	}
	if len(report.PackageValues[0].Values) != 0 {
		t.Fatal("schema annotations mutated package input")
	}
}

func TestStaticPackageInventoryMetadataFallback(t *testing.T) {
	policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{
		{PackageName: "declared", Files: []string{"values.yaml"}, Schema: map[string]interface{}{"properties": map[string]interface{}{"empty": map[string]interface{}{}}}},
		{PackageName: "opaque", Files: []string{"values.yaml"}, Schema: map[string]interface{}{"$ref": "other.json"}},
	}})
	entries := policy.catalog().packages
	if len(entries) != 2 || entries[0].path != "empty" || entries[1].path != "(package values)" {
		t.Fatalf("schema inventory should replace only redundant metadata placeholder: %+v", entries)
	}
}

func TestStaticBundleInventoryIncludesObservedMissingPaths(t *testing.T) {
	later := introspect.ValueUsageLocation{File: "z-values.yaml", Line: 9, Column: 2, Kind: "bundleVariable"}
	earlier := introspect.ValueUsageLocation{File: "a-values.yaml", Line: 2, Column: 3, Kind: "bundleVariable"}
	report := introspect.InspectionReport{
		Bundle: &introspect.BundleMetadata{Format: "next", Variables: map[string]interface{}{
			"app": map[string]interface{}{"enabled": false, "nullable": nil}, "configured": 0,
		}},
		PackageValues: []introspect.PackageValues{
			{PackageName: "first", Sources: []introspect.ValueSource{{References: []introspect.ValueReference{
				{Scope: "bundle-vars", Path: ".missing", Status: introspect.ValueMissing, Locations: []introspect.ValueUsageLocation{later, earlier}},
				{Scope: "bundle-vars", Path: ".app.enabled", Locations: []introspect.ValueUsageLocation{earlier}},
				{Scope: "bundle-vars", Path: ".app.nullable", Locations: []introspect.ValueUsageLocation{earlier}},
				{Scope: "bundle-vars", Path: ".app.missing", Computed: true, Locations: []introspect.ValueUsageLocation{later}},
				{Scope: "package-values", Path: ".ignored", Locations: []introspect.ValueUsageLocation{earlier}},
			}}}},
			{PackageName: "second", Sources: []introspect.ValueSource{{References: []introspect.ValueReference{
				{Scope: "bundle-vars", Path: "missing", Locations: []introspect.ValueUsageLocation{later}},
				{Scope: "bundle-vars", Path: ".configured", Locations: []introspect.ValueUsageLocation{earlier}},
			}}}},
		},
	}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	model := Model{report: report, bundle: report.Bundle}
	items := model.bundleVariableItems()
	var paths []string
	for _, item := range items {
		paths = append(paths, item.Path)
		if !item.IsBundleVariable || item.Component != "UDS Next bundle" {
			t.Fatalf("variable identity lost: %+v", item)
		}
		present := item.Path != "missing" && item.Path != "app.missing"
		if item.ValuePresent != present || item.DefaultPresent != present {
			t.Fatalf("observed path presence changed: %+v", item)
		}
		if !present && item.DefaultValue != nil {
			t.Fatalf("missing reference invented a value: %+v", item)
		}
	}
	if want := []string{"app", "app.enabled", "app.missing", "app.nullable", "configured", "missing"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("bundle inventory paths = %v, want %v", paths, want)
	}
	if items[1].DefaultValue != false || items[3].DefaultValue != nil || items[4].DefaultValue != 0 {
		t.Fatal("false, null or zero input changed")
	}
	if !reflect.DeepEqual(items[5].Locations, []introspect.ValueUsageLocation{earlier, later}) {
		t.Fatalf("reference locations not combined/deduplicated deterministically: %+v", items[5].Locations)
	}
	if !reflect.DeepEqual(model.bundleVariableItems(), items) {
		t.Fatal("repeated inventory changed ordering or metadata")
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("bundle inventory mutated source references")
	}
	if items := (Model{}).bundleVariableItems(); len(items) != 0 {
		t.Fatal("model without bundle invented variables")
	}
}

func TestStaticPackageInventoryGenericTemplatesDoNotClaimEveryInput(t *testing.T) {
	for _, kind := range []string{"runtimeTemplate", "unparsedTemplate", "unavailableTemplate", "templatedValues", "bundleValuesTemplate", "setValues", "unknown", ""} {
		t.Run(kind, func(t *testing.T) {
			usage := introspect.PackageValueUsage{Path: ".", Runtime: true}
			if kind != "" {
				usage.Locations = []introspect.ValueUsageLocation{{File: "template.yaml", Kind: kind}}
			}
			policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{
				PackageName: "core", Values: map[string]interface{}{"enabled": false, "app": map[string]interface{}{"replicas": 3}},
				Schema: map[string]interface{}{"properties": map[string]interface{}{"declared": true}}, Usages: []introspect.PackageValueUsage{usage},
			}}})
			for _, path := range []string{"enabled", "app.replicas", "declared"} {
				entry := policyEntry(t, policy.catalog(), "core", path)
				if len(entry.routes) != 0 || entry.status != "unmapped" {
					t.Fatalf("generic template attached to unrelated package input %s: %+v", path, entry)
				}
				if entry.present && (entry.authoring == nil || entry.authoring.basis != authoringSupplied) {
					t.Fatalf("generic template blocked a known supplied value: %+v", entry)
				}
			}
			refs := policyDeclarations(policy.catalog(), "core")
			if len(refs) != 1 || refs[0].usage.Path != "." || refs[0].hasLeaf || refs[0].sourcePresent {
				t.Fatalf("generic template must retain its own reference row: %+v", refs)
			}
		})
	}
}

func TestStaticPackageInventoryRuntimeContextsDoNotCollideWithPackageKeys(t *testing.T) {
	for _, context := range []string{"Variables", "State", "Constants", "Pkg"} {
		t.Run(context, func(t *testing.T) {
			path := context + ".CA_BUNDLE_CERTS"
			usage := introspect.PackageValueUsage{Path: "." + path, Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "template.yaml", Kind: "runtimeContext"}}}
			policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "core",
				Values: map[string]interface{}{context: map[string]interface{}{"CA_BUNDLE_CERTS": "package input"}},
				Usages: []introspect.PackageValueUsage{usage},
			}}})
			entry := policyEntry(t, policy.catalog(), "core", path)
			if len(entry.routes) != 0 || entry.status != "unmapped" || entry.authoring == nil {
				t.Fatalf("non-.Values context bound identically named package input: %+v", entry)
			}
			refs := policyDeclarations(policy.catalog(), "core")
			if len(refs) != 1 || refs[0].usage.Path != usage.Path || refs[0].hasLeaf || refs[0].sourcePresent {
				t.Fatalf("context reference lost its independent identity: %+v", refs)
			}
			if route, ok := refs[0].route(path); ok || route.Target != "" {
				t.Fatalf("context reference fabricated a package route: %+v", route)
			}
		})
	}
}

func TestStaticPackageInventoryProvenValueReferencesKeepInputRelations(t *testing.T) {
	for _, kind := range []string{"packageValue", "field", "range", "with", "index", "value", "variable"} {
		t.Run(kind, func(t *testing.T) {
			usage := introspect.PackageValueUsage{Path: ".setting", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "template.yaml", Kind: kind}}}
			policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "core",
				Values: map[string]interface{}{"setting": false}, Usages: []introspect.PackageValueUsage{usage},
			}}})
			entry := policyEntry(t, policy.catalog(), "core", "setting")
			if len(entry.routes) != 1 || entry.routes[0].Status != "runtime" || entry.routes[0].Target != "" || len(policyDeclarations(policy.catalog(), "core")) != 0 {
				t.Fatalf("proven .Values reference lost input relation or invented a Helm target: %+v", entry)
			}
		})
	}
	usage := introspect.PackageValueUsage{Path: ".setting", TargetPath: ".config.setting", ComponentName: "server", ChartName: "web"}
	policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "core",
		Values: map[string]interface{}{"setting": false}, Usages: []introspect.PackageValueUsage{usage},
	}}})
	entry := policyEntry(t, policy.catalog(), "core", "setting")
	if len(entry.routes) != 1 || entry.routes[0].Target != "config.setting" || entry.routes[0].Status != "mapped" {
		t.Fatalf("explicit Helm target lost its declared mapping: %+v", entry)
	}
}
