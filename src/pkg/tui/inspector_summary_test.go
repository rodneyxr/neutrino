package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func inspectorRuntimeFixture(t *testing.T, path, kind string, line int) (Model, DisplayItem, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "neutrino-cache/packages/sha256-abcdef/components/uds-operator/values/uds-operator-config-0.1.0-0")
	usage := introspect.PackageValueUsage{
		Path: path, ComponentName: "operator", ChartName: "operator", Runtime: true, Status: introspect.ValueDynamic,
		Locations: []introspect.ValueUsageLocation{{File: file, Line: line, Kind: kind}},
	}
	values := map[string]interface{}{"replicas": 3}
	report := introspect.InspectionReport{
		Bundle: &introspect.BundleMetadata{Format: "next", Name: "demo", Variables: map[string]interface{}{"scale": 3},
			Packages: []introspect.BundlePackageMetadata{{ID: "core", DependsOn: []string{"bootstrap"}}}},
		Components: map[string]*introspect.IntrospectResult{"core/operator/operator": {
			Values: []introspect.ValueUsage{{Path: "certs"}}, PackageValueMappings: []introspect.PackageValueUsage{usage},
		}},
		ComponentOrder: []string{"core/operator/operator"},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Values: values,
			Files: []string{"defaults.yaml", "bundle-values.yaml"}, Usages: []introspect.PackageValueUsage{usage},
			Sources: []introspect.ValueSource{
				{ID: "packaged:1", Name: "defaults.yaml", Kind: "packaged-default", Order: 1, Status: introspect.ValueResolved, Values: values},
				{ID: "bundle:2", Name: "bundle-values.yaml", Kind: "bundle-values", Order: 2, Status: introspect.ValueResolved, Values: values,
					MaterializedFile: "/tmp/neutrino-cache/bundle-rendered-values.yaml",
					References: []introspect.ValueReference{{Scope: "bundle-vars", Path: ".scale", Status: introspect.ValueResolved,
						Locations: []introspect.ValueUsageLocation{{File: "bundle-values.yaml", Line: 2, Kind: "bundleVariable"}},
					}}},
			},
		}},
	}
	model := NewModel(report)
	for _, entry := range model.valuePolicy.catalog().charts["core/operator/operator"] {
		if entry.mapping != nil && entry.path == path {
			value := packageValueItem(entry, "Zarf Package Values")
			model.focusPane, model.detailRef = FocusInspector, &value
			model.windowWidth, model.windowHeight = 80, 24
			model.buildDetailItems()
			return model, value, file
		}
	}
	t.Fatalf("runtime declaration %s missing from catalog", path)
	return Model{}, DisplayItem{}, ""
}

func inspectorSummaryText(model Model) string {
	body, _ := model.inspectorContent(80, true)
	return ansi.Strip(body)
}

func TestInspectorSummaryRuntimeContextStaysCompact(t *testing.T) {
	model, value, file := inspectorRuntimeFixture(t, ".Variables.CA_BUNDLE_CERTS", "runtimeContext", 37)
	body := inspectorSummaryText(model)
	for _, want := range []string{".Variables.CA_BUNDLE_CERTS", "Zarf runtime reference", "Requires deployment context", "Files & references (1)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing runtime summary %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{".Values.Variables", "Helm .Values", "chart root", "→", "->", "bundle-rendered", "Input history", "dependencies", ".vars.scale", "bootstrap"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("runtime summary disclosed unrelated or fabricated detail %q:\n%s", unwanted, body)
		}
	}
	if !strings.Contains(body, filepath.Base(file)+":37") {
		t.Fatalf("runtime summary hides its actual filename and line: %s", body)
	}
	if lines := strings.Count(body, "\n") + 1; lines > 12 {
		t.Fatalf("runtime summary should fit without scrolling at 80 columns: %d lines\n%s", lines, body)
	}
	if model.hasValueHistory(value) || model.canViewFullValue(value) {
		t.Fatal("runtime reference offered a resolved-value or package-history action")
	}
	for _, detail := range model.detailItems {
		if detail.Kind == DetailItemMapping || detail.Kind == DetailItemTrace || detail.Kind == DetailItemPanel && detail.panel != detailSources {
			t.Fatalf("runtime summary offered fabricated dataflow or history: %+v", detail)
		}
	}
}

func TestInspectorSummaryStaticExpressionsAreNotDeploymentContexts(t *testing.T) {
	for _, sample := range []struct{ name, path, kind string }{
		{"function-only", ".", "runtimeTemplate"},
		{"package-value", ".settings.enabled", "packageValue"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			model, _, _ := inspectorRuntimeFixture(t, sample.path, sample.kind, 0)
			body := inspectorSummaryText(model)
			if !strings.Contains(body, "Expression not evaluated") || strings.Contains(body, "Requires deployment context") {
				t.Fatalf("static template expression was labeled as requiring deployment context:\n%s", body)
			}
			for _, unwanted := range []string{"Helm .Values", "chart root", "→", "Input history", ".Values.Variables"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("expression summary invented %q:\n%s", unwanted, body)
				}
			}
		})
	}
}

func TestInspectorSummarySourceNavigationSeparatesLabelAndExactPath(t *testing.T) {
	for _, line := range []int{0, 37} {
		model, _, file := inspectorRuntimeFixture(t, ".Variables.CA_BUNDLE_CERTS", "runtimeContext", line)
		sourcesIndex := -1
		for i, detail := range model.detailItems {
			if detail.Kind == DetailItemPanel && detail.panel == detailSources {
				sourcesIndex = i
			}
		}
		if sourcesIndex < 0 {
			sourcesIndex = 0
		}
		model.detailCursor = sourcesIndex
		if command := model.openSelectedSource(); command != nil || model.detailPanel != detailSources {
			t.Fatal("Sources should open its list without launching an editor")
		}
		body := inspectorSummaryText(model)
		if !strings.Contains(body, "Chart values template") || !strings.Contains(strings.ReplaceAll(body, "\n  ", ""), filepath.Base(file)) || strings.Contains(body, ":0:0") {
			t.Fatalf("source list should show a friendly label with known coordinates only:\n%s", body)
		}
		if len(model.detailItems) != 1 || model.detailItems[0].Kind != DetailItemSource || model.detailItems[0].File != file {
			t.Fatalf("friendly source lost exact source identity: %+v", model.detailItems)
		}
		if line == 0 && !strings.Contains(body, "line unavailable") || line > 0 && !strings.Contains(body, ":37") {
			t.Fatalf("source location label fabricated or dropped coordinates:\n%s", body)
		}
		if command := model.activateFocused(); command != nil || model.detailPanel != detailSourceFile {
			t.Fatal("source action should open secondary details before launching an editor")
		}
		value, _ := model.detailValue()
		doc := model.inspectorDocument(value)
		foundExact := false
		for _, row := range doc.lines {
			if row.label == "Location" {
				foundExact = row.text == sourceCoordinateLabel(file, line)
			}
		}
		if !foundExact || strings.Contains(inspectorSummaryText(model), ":0:0") {
			t.Fatalf("source details lost exact path or fabricated zero coordinates: %+v", doc)
		}
		if len(model.detailItems) != 0 || !strings.Contains(inspectorSummaryText(model), "Source not available locally") {
			t.Fatalf("unavailable source should explain why no editor action is offered: %+v", model.detailItems)
		}
		if !model.backDetailPanel() || model.detailPanel != detailSources || !model.backDetailPanel() || model.detailPanel != detailSummary || model.detailCursor != sourcesIndex {
			t.Fatal("source navigation did not restore Sources then summary selection")
		}
	}
}

func TestInspectorSummaryExistingSourceOffersExactEditorTarget(t *testing.T) {
	model, _, file := inspectorRuntimeFixture(t, ".Variables.CA_BUNDLE_CERTS", "runtimeContext", 37)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("\n", 36)+"certs: '{{ .Variables.CA_BUNDLE_CERTS }}'\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	model.openDetailPanel(detailSources, nil)
	if len(model.detailItems) != 1 || model.detailItems[0].Kind != DetailItemSource {
		t.Fatalf("existing source not listed: %+v", model.detailItems)
	}
	if command := model.activateFocused(); command != nil || model.detailPanel != detailSourceFile {
		t.Fatal("source selection should show details before launching an editor")
	}
	if len(model.detailItems) != 1 || model.detailItems[0].Kind != DetailItemFile || model.detailItems[0].File != file || model.detailItems[0].Location.Line != 37 {
		t.Fatalf("existing source editor action lost exact path/known line: %+v", model.detailItems)
	}
	if strings.Contains(inspectorSummaryText(model), "Source not available locally") {
		t.Fatal("existing source incorrectly reported unavailable")
	}
}

func TestInspectorSummarySchemaDefaultRemainsAnnotation(t *testing.T) {
	const annotation = "schema annotation"
	for _, sample := range []struct {
		name, text string
		present    bool
		value      interface{}
	}{
		{name: "unset"}, {name: "null", text: "null", present: true}, {name: "false", text: "false", present: true, value: false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			values := map[string]interface{}{}
			if sample.present {
				values["setting"] = sample.value
			}
			model := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "core", Values: values,
				Schema: map[string]interface{}{"required": []interface{}{"setting"}, "properties": map[string]interface{}{
					"setting": map[string]interface{}{"default": annotation},
				}},
			}}})
			value := packageValueItem(policyEntry(t, model.valuePolicy.catalog(), "core", "setting"), "Zarf Package Values")
			model.focusPane, model.detailRef = FocusInspector, &value
			model.buildDetailItems()
			doc := model.inspectorDocument(value)
			fields := make(map[string]string)
			for _, line := range doc.lines {
				fields[line.label] = line.text
			}
			if !strings.Contains(fields["Schema default"], annotation) || !strings.Contains(fields["Schema default"], "annotation only") || fields["Schema"] != "Required in parent object" {
				t.Fatalf("schema annotations lost: %+v", fields)
			}
			if !sample.present {
				if fields["Input"] != "Not supplied" || fields["Inspected input"] != "" || model.canViewFullValue(value) {
					t.Fatalf("schema default became supplied input: %+v", fields)
				}
			} else if fields["Input"] != "" || fields["Inspected input"] != sample.text || !model.canViewFullValue(value) {
				t.Fatalf("explicit null/false became unset or schema default: %+v", fields)
			}
		})
	}
}

func TestInspectorSummaryWildcardRemainsUnresolved(t *testing.T) {
	value := DisplayItem{Component: "server/web", ValueUsage: introspect.ValueUsage{Path: "apps.*.port", Wildcard: true,
		DefaultValue: 8080, DefaultPresent: true, Schema: map[string]interface{}{"default": 9090}}}
	model := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"server/web": {Values: []introspect.ValueUsage{value.ValueUsage}}}})
	model.focusPane, model.detailRef = FocusInspector, &value
	model.buildDetailItems()
	body := inspectorSummaryText(model)
	if !strings.Contains(body, "Helm dynamic reference") || !strings.Contains(body, "Dynamic path · not evaluated") || strings.Contains(body, "8080") || strings.Contains(body, "9090") || strings.Contains(body, "Schema default") {
		t.Fatalf("wildcard summary pretended to resolve a concrete value:\n%s", body)
	}
	if !model.valueUnresolved(value) || model.canViewFullValue(value) {
		t.Fatal("wildcard was treated as a resolved configurable value")
	}
}

func TestInspectorSummaryMappingStatePrecedesLongPath(t *testing.T) {
	for _, sample := range []struct {
		value   interface{}
		present bool
		state   string
	}{{nil, false, "Not supplied"}, {nil, true, "null"}, {false, true, "false"}} {
		values := map[string]interface{}{}
		if sample.present {
			values["very_long_package_input_path"] = sample.value
		}
		model := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"core/server/web": {Values: []introspect.ValueUsage{{Path: "enabled", DefaultValue: true}}}},
			PackageValues: []introspect.PackageValues{{PackageName: "core", Values: values, Usages: []introspect.PackageValueUsage{{Path: ".very_long_package_input_path", TargetPath: ".enabled", ComponentName: "server", ChartName: "web"}}}}})
		value := DisplayItem{Component: "core/server/web", ValueUsage: introspect.ValueUsage{Path: "enabled", DefaultValue: true}}
		model.focusPane, model.detailRef = FocusInspector, &value
		model.buildDetailItems()
		for _, width := range []int{36, 76} {
			body, _ := model.inspectorContent(width, true)
			if !strings.Contains(body, "Setting input") || !strings.Contains(body, sample.state) {
				t.Fatalf("width %d hid mapping state %s: %s", width, sample.state, body)
			}
		}
	}
}

func TestInspectorSummaryNarrowDescriptionRemainsAccessible(t *testing.T) {
	const description = "A modest description whose final phrase stays available."
	model := NewModel(introspect.InspectionReport{})
	value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "setting", Schema: map[string]interface{}{"description": description}}}
	model.windowWidth, model.windowHeight = 40, 12
	model.focusPane, model.detailRef = FocusInspector, &value
	model.buildDetailItems()
	found := false
	for i, item := range model.detailItems {
		if item.Kind == DetailItemPanel && item.panel == detailDescription {
			model.detailCursor = i
			model.activateFocused()
			found = true
			break
		}
	}
	if !found {
		t.Fatal("clipped narrow description has no disclosure action")
	}
	doc := model.inspectorDocument(value)
	if len(doc.lines) != 1 || doc.lines[0].text != description {
		t.Fatal("description disclosure lost authored content")
	}
	model.backDetailPanel()
	if model.detailPanel != detailSummary {
		t.Fatal("description Escape did not restore summary")
	}
}
