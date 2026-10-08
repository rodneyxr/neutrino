package tui

import (
	"reflect"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func policyEntry(t *testing.T, catalog *packageValuesCatalog, pkg, path string) packageValueEntry {
	t.Helper()
	for _, entry := range catalog.packages {
		if entry.pkg != nil && entry.pkg.PackageName == pkg && entry.path == path && entry.mapping == nil {
			return entry
		}
	}
	t.Fatalf("package entry %s/%s missing", pkg, path)
	return packageValueEntry{}
}

func policyRow(t *testing.T, table *mappingTable, path string) mappingRow {
	t.Helper()
	for _, row := range table.rows {
		if row.path == path {
			return row
		}
	}
	t.Fatalf("mapping row %s missing", path)
	return mappingRow{}
}

func policyDeclarations(catalog *packageValuesCatalog, pkg string) []*valueMapping {
	var refs []*valueMapping
	for _, entry := range catalog.packages {
		if entry.pkg != nil && entry.pkg.PackageName == pkg && entry.mapping != nil {
			refs = append(refs, entry.mapping)
		}
	}
	return refs
}

func TestPackageValuesPolicyOwnershipAndDeclarationIdentity(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	excluded := usage
	excluded.ExcludePaths = []string{".app.enabled"}
	value := introspect.ValueUsage{Path: "config.enabled", DefaultValue: true}
	policy := newPackageValuesPolicy(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"a.v1/server/web": {Values: []introspect.ValueUsage{value}, PackageValueMappings: []introspect.PackageValueUsage{usage, excluded}},
			"b.v2/server/web": {Values: []introspect.ValueUsage{value}, PackageValueMappings: []introspect.PackageValueUsage{usage}},
			"server/web":      {Values: []introspect.ValueUsage{value}},
		},
		PackageValues: []introspect.PackageValues{
			{PackageName: "a.v1", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": "a"}}, Usages: []introspect.PackageValueUsage{usage, excluded}},
			{PackageName: "b.v2", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": "b"}}, Usages: []introspect.PackageValueUsage{usage}},
		},
	})
	catalog := policy.catalog()
	a := policyDeclarations(catalog, "a.v1")
	b := policyDeclarations(catalog, "b.v2")
	if len(a) != 2 || len(b) != 1 || a[0] == a[1] || a[0] == b[0] {
		t.Fatalf("declaration occurrences lost: a=%v b=%v", a, b)
	}
	for _, tc := range []struct {
		ref                  *valueMapping
		owner, input, status string
	}{{a[0], "a.v1", "a", "available"}, {a[1], "a.v1", "a", "excluded"}, {b[0], "b.v2", "b", "available"}} {
		table, err := policy.open(tc.ref)
		if err != nil {
			t.Fatal(err)
		}
		row := policyRow(t, table, value.Path)
		if table.mapping.pkg.PackageName != tc.owner || table.mapping.chartKey != tc.owner+"/server/web" || row.input != tc.input || row.status != tc.status {
			t.Fatalf("mapping crossed owners or lost reference: %+v", table)
		}
	}
	for _, owner := range []string{"a.v1", "b.v2"} {
		details := policy.details(owner+"/server/web", value)
		if len(details) != len(policyDeclarations(catalog, owner)) {
			t.Fatalf("%s reverse declaration count: %d", owner, len(details))
		}
		for i, detail := range details {
			if detail.pkg.PackageName != owner || detail.mapping != policyDeclarations(catalog, owner)[i] {
				t.Fatalf("%s reverse ownership/identity lost: %+v", owner, detail)
			}
		}
	}
	if details := policy.details("server/web", value); len(details) != 0 {
		t.Fatalf("bare chart stole declarations from canonical prefixed charts: %+v", details)
	}
	if len(catalog.charts["a.v1/server/web"]) != 2 || len(catalog.charts["b.v2/server/web"]) != 1 {
		t.Fatal("chart declaration occurrences lost")
	}
}

func TestPackageValuesPolicySegmentTransformsAndExclusions(t *testing.T) {
	root := introspect.PackageValueUsage{Path: ".", TargetPath: ".config", ComponentName: "server", ChartName: "root", ExcludePaths: []string{".app.secret", ".app.secret.child"}}
	subtree := introspect.PackageValueUsage{Path: ".app", TargetPath: ".", ComponentName: "server", ChartName: "subtree", ExcludePaths: []string{".app.secret", ".app.secret.child"}}
	policy := newPackageValuesPolicy(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"server/root":    {Values: []introspect.ValueUsage{{Path: "config.app.replicas"}, {Path: "config.app.secret.child"}, {Path: "config.app.secretExtra"}, {Path: "configuration.other"}}},
			"server/subtree": {Values: []introspect.ValueUsage{{Path: "replicas"}, {Path: "secret.child"}, {Path: "secretExtra"}}},
		},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{
			"app":         map[string]interface{}{"replicas": 0, "secret": map[string]interface{}{"child": false}, "secretExtra": true},
			"application": map[string]interface{}{"enabled": true},
		}, Usages: []introspect.PackageValueUsage{root, subtree}}},
	})
	catalog := policy.catalog()
	entry := policyEntry(t, catalog, "core", "app.replicas")
	if entry.status != "mapped" || len(entry.routes) != 2 || entry.routes[0].Target != "config.app.replicas" || entry.routes[1].Target != "replicas" {
		t.Fatalf("root/subtree routes: %+v", entry)
	}
	if entry := policyEntry(t, catalog, "core", "app.secret.child"); entry.status != "excluded" || entry.authoring != nil {
		t.Fatalf("excluded descendant authoring: %+v", entry)
	}
	if entry := policyEntry(t, catalog, "core", "app.secretExtra"); entry.status != "mapped" {
		t.Fatalf("exclusion matched partial segment: %+v", entry)
	}
	if entry := policyEntry(t, catalog, "core", "application.enabled"); len(entry.routes) != 1 {
		t.Fatalf("source matched partial segment: %+v", entry)
	}
	refs := policyDeclarations(catalog, "core")
	for i, paths := range [][]string{{"config.app.replicas", "config.app.secret.child", "config.app.secretExtra"}, {"replicas", "secret.child", "secretExtra"}} {
		table, err := policy.open(refs[i])
		if err != nil {
			t.Fatal(err)
		}
		for j, path := range paths {
			row := policyRow(t, table, path)
			if row.packagePath != []string{".app.replicas", ".app.secret.child", ".app.secretExtra"}[j] || !row.present {
				t.Fatalf("reverse segment transform: %+v", row)
			}
			if j == 1 && (row.status != "excluded" || row.exclusion != ".app.secret" || row.authoring != nil) {
				t.Fatalf("ordered exclusion spelling/authoring lost: %+v", row)
			}
		}
		if i == 0 && (table.inside != len(table.rows)-1 || table.rows[len(table.rows)-1].path != "configuration.other" || table.rows[len(table.rows)-1].authoring != nil) {
			t.Fatalf("outside paths must be last and blocked: %+v", table)
		}
	}
	rootPolicy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{"app": map[string]interface{}{}}, Usages: []introspect.PackageValueUsage{subtree}}}})
	if routes := policyEntry(t, rootPolicy.catalog(), "core", "app").routes; len(routes) != 1 || routes[0].Target != "." {
		t.Fatalf("source itself must map to chart root: %+v", routes)
	}
}

func TestPackageValuesPolicyInputAndAuthoringPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input interface{}
	}{{"null", nil}, {"false", false}, {"zero", 0}, {"empty-string", ""}, {"empty-object", map[string]interface{}{}}, {"empty-array", []interface{}{}}} {
		t.Run(tc.name, func(t *testing.T) {
			usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
			values := []introspect.ValueUsage{{Path: "config.supplied", DefaultValue: "wrong"}, {Path: "config.default", DefaultValue: false}, {Path: "config.unknown"}, {Path: "config.supplied.child", DefaultValue: 0}}
			policy := newPackageValuesPolicy(introspect.InspectionReport{
				Components:    map[string]*introspect.IntrospectResult{"server/web": {Values: values}},
				PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{"app": map[string]interface{}{"supplied": tc.input}}, Usages: []introspect.PackageValueUsage{usage}}},
			})
			table, err := policy.open(policyDeclarations(policy.catalog(), "core")[0])
			if err != nil {
				t.Fatal(err)
			}
			row := policyRow(t, table, "config.supplied")
			if !row.present || !reflect.DeepEqual(row.input, tc.input) || row.authoring == nil || row.authoring.basis != authoringSupplied || !reflect.DeepEqual(row.authoring.value, tc.input) || row.authoring.path != ".app.supplied" {
				t.Fatalf("explicit input replaced by chart default: %+v", row)
			}
			for _, expected := range []struct {
				path  string
				basis authoringBasis
				value interface{}
			}{{"config.default", authoringDefault, false}, {"config.unknown", authoringUnknown, nil}, {"config.supplied.child", authoringDefault, 0}} {
				row := policyRow(t, table, expected.path)
				if row.present || row.authoring == nil || row.authoring.basis != expected.basis || !reflect.DeepEqual(row.authoring.value, expected.value) {
					t.Fatalf("absent input/default precedence: %+v", row)
				}
			}
			for _, value := range values {
				details := policy.details("server/web", value)
				if len(details) != 1 || !reflect.DeepEqual(details[0].row, policyRow(t, table, value.Path)) {
					t.Fatalf("forward/reverse authoring diverged for %s: %+v", value.Path, details)
				}
			}
		})
	}
}

func TestPackageValuesPolicyBlockedAndChartlessAuthoring(t *testing.T) {
	base := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	excluded, runtime := base, base
	excluded.ExcludePaths = []string{".app.enabled"}
	runtime.Runtime = true
	for _, tc := range []struct {
		name, status string
		usages       []introspect.PackageValueUsage
	}{{"excluded", "excluded", []introspect.PackageValueUsage{excluded}}, {"runtime", "runtime", []introspect.PackageValueUsage{runtime}}, {"mixed", "mixed", []introspect.PackageValueUsage{base, excluded}}} {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPackageValuesPolicy(introspect.InspectionReport{
				Components:    map[string]*introspect.IntrospectResult{"server/web": {Values: []introspect.ValueUsage{{Path: "config.enabled", DefaultValue: true}, {Path: "elsewhere", DefaultValue: true}}}},
				PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}}, Usages: tc.usages}},
			})
			entry := policyEntry(t, policy.catalog(), "core", "app.enabled")
			if entry.status != tc.status || entry.authoring != nil {
				t.Fatalf("blocked aggregate offered authoring: %+v", entry)
			}
			for _, detail := range policy.details("server/web", introspect.ValueUsage{Path: "config.enabled", DefaultValue: true}) {
				if detail.row.status != "available" && detail.row.authoring != nil {
					t.Fatalf("blocked mapping offered authoring: %+v", detail)
				}
			}
			for _, detail := range policy.details("server/web", introspect.ValueUsage{Path: "elsewhere", DefaultValue: true}) {
				if detail.row.status != "other" || detail.row.authoring != nil {
					t.Fatalf("outside mapping offered authoring: %+v", detail)
				}
			}
		})
	}
	files, sources := []string{"packaged.yaml", "operator-second.yaml", "operator-first.yaml"}, []string{"package", "second", "first"}
	policy := newPackageValuesPolicy(introspect.InspectionReport{PackageValues: []introspect.PackageValues{
		{PackageName: "chartless", Values: map[string]interface{}{"z": 0, "a": false}, Files: files, FileSources: sources},
		{PackageName: "metadata", Files: files, FileSources: sources},
	}})
	catalog := policy.catalog()
	if len(catalog.packages) != 3 || catalog.packages[0].path != "a" || catalog.packages[1].path != "z" {
		t.Fatalf("chartless leaves not deterministically sorted: %+v", catalog.packages)
	}
	for _, path := range []string{"a", "z"} {
		entry := policyEntry(t, catalog, "chartless", path)
		if entry.status != "unmapped" || entry.authoring == nil || entry.authoring.basis != authoringSupplied || !reflect.DeepEqual(entry.pkg.Files, files) || !reflect.DeepEqual(entry.pkg.FileSources, sources) {
			t.Fatalf("chartless input/provenance lost: %+v", entry)
		}
	}
	entry := policyEntry(t, catalog, "metadata", "(package values)")
	if entry.present || entry.mapping != nil || entry.authoring != nil || !reflect.DeepEqual(entry.pkg.Files, files) || !reflect.DeepEqual(entry.pkg.FileSources, sources) {
		t.Fatalf("metadata pretends to be supplied input: %+v", entry)
	}
}

func TestPackageValuesPolicyPreservesDiscoveryAndSuppliedOnlyRows(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	value := introspect.ValueUsage{Path: "config.enabled", Chart: "web", Wildcard: true, DefaultValue: true, Locations: []introspect.ValueUsageLocation{{File: "templates/config.yaml", Line: 7, Column: 4}}}
	policy := newPackageValuesPolicy(introspect.InspectionReport{
		Components:    map[string]*introspect.IntrospectResult{"server/web": {Values: []introspect.ValueUsage{value}}},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false, "extra": "supplied"}}, Usages: []introspect.PackageValueUsage{usage}}},
	})
	table, err := policy.open(policyDeclarations(policy.catalog(), "core")[0])
	if err != nil {
		t.Fatal(err)
	}
	if row := policyRow(t, table, value.Path); !reflect.DeepEqual(row.value, value) || row.undiscovered {
		t.Fatalf("original discovered value metadata changed: %+v", row)
	}
	row := policyRow(t, table, "config.extra")
	if !row.undiscovered || !row.present || row.input != "supplied" || row.defaultValue != nil || !reflect.DeepEqual(row.value, introspect.ValueUsage{Path: "config.extra"}) || row.authoring == nil || row.authoring.basis != authoringSupplied {
		t.Fatalf("supplied-only row invented chart metadata: %+v", row)
	}
}

func TestPackageValuesPolicyChartDeclarationsAndUnavailableTargets(t *testing.T) {
	owned := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web", ExcludePaths: []string{".app.secret", ".app.hidden"}}
	chartUsage := owned
	chartUsage.Locations = []introspect.ValueUsageLocation{{File: "zarf.yaml", Line: 19, Column: 3}}
	missing := introspect.PackageValueUsage{Path: ".missing", TargetPath: ".config", ComponentName: "server", ChartName: "missing"}
	orphan := introspect.PackageValueUsage{Path: ".orphan", TargetPath: ".", ComponentName: "server", ChartName: "orphan"}
	policy := newPackageValuesPolicy(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{
			"server/web":    {Values: []introspect.ValueUsage{{Path: "config.enabled"}}, PackageValueMappings: []introspect.PackageValueUsage{chartUsage}},
			"server/orphan": {PackageValueMappings: []introspect.PackageValueUsage{orphan}},
		},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Usages: []introspect.PackageValueUsage{owned, missing}}},
	})
	catalog := policy.catalog()
	entries := catalog.charts["server/web"]
	if len(entries) != 1 || entries[0].mapping == nil {
		t.Fatalf("chart declaration reference lost: %+v", entries)
	}
	for _, entry := range entries {
		if !reflect.DeepEqual(entry.mapping.usage, chartUsage) || entry.pkg == nil || entry.pkg.PackageName != "core" {
			t.Fatalf("chart spelling/metadata or resolved owner changed: %+v", entry)
		}
		table, err := policy.open(entry.mapping)
		if err != nil || table.mapping != entry.mapping || table.sourcePresent {
			t.Fatalf("chart declaration did not resolve absent package source: table=%+v err=%v", table, err)
		}
	}
	if _, err := policy.open(policyDeclarations(catalog, "core")[1]); err == nil || err.Error() != "Destination chart for this mapping is unavailable" {
		t.Fatalf("missing destination error: %v", err)
	}
	if entries := catalog.charts["server/orphan"]; len(entries) != 1 || entries[0].mapping == nil {
		t.Fatalf("orphan chart declaration hidden: %+v", entries)
	} else if _, err := policy.open(entries[0].mapping); err == nil || err.Error() != "Package for this mapping is unavailable" {
		t.Fatalf("missing package error: %v", err)
	}
}

func TestPackageValuesPolicyDeclarationVisibility(t *testing.T) {
	mapping := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	runtime := introspect.PackageValueUsage{Path: ".runtime", Runtime: true}
	for _, tc := range []struct {
		name          string
		values        map[string]interface{}
		declarations  int
		sourcePresent bool
	}{
		{"absent", map[string]interface{}{}, 2, false},
		{"empty", map[string]interface{}{"app": map[string]interface{}{}}, 2, true},
		{"runtime-supplied", map[string]interface{}{"runtime": false}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := newPackageValuesPolicy(introspect.InspectionReport{
				Components:    map[string]*introspect.IntrospectResult{"server/web": {Values: []introspect.ValueUsage{{Path: "config.enabled", DefaultValue: true}}}},
				PackageValues: []introspect.PackageValues{{PackageName: "core", Values: tc.values, Usages: []introspect.PackageValueUsage{mapping, runtime}}},
			})
			refs := policyDeclarations(policy.catalog(), "core")
			if len(refs) != tc.declarations {
				t.Fatalf("runtime reference suppression hid a declaration or duplicated supplied reference: %d", len(refs))
			}
			table, err := policy.open(refs[0])
			if err != nil {
				t.Fatal(err)
			}
			if table.sourcePresent != tc.sourcePresent || table.inside != 1 || table.rows[0].present {
				t.Fatalf("absent/empty source must retain discovered destination: %+v", table)
			}
		})
	}
}
