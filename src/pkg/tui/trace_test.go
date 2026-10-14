package tui

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
)

func traceFixtureReport() introspect.InspectionReport {
	usage := introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"}
	return introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"core/server/web": {
			Values: []introspect.ValueUsage{{Path: "config.enabled", DefaultValue: true}},
		}}, ComponentOrder: []string{"core/server/web"},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Usages: []introspect.PackageValueUsage{usage},
			Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}},
			Sources: []introspect.ValueSource{
				{ID: "packaged:1", Kind: "packaged-default", Name: "defaults.yaml", MaterializedFile: "/fixture/defaults.yaml", Status: introspect.ValueResolved,
					Values: map[string]interface{}{"app": map[string]interface{}{"enabled": true}}},
				{ID: "operator:2", Kind: "operator-file", Name: "operator.yaml", MaterializedFile: "/fixture/operator.yaml", Status: introspect.ValueResolved,
					Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}}},
			},
		}},
	}
}

func traceFixtureValue(model Model) DisplayItem {
	return packageValueItem(model.valuePolicy.catalog().packages[0], "Zarf Package Values")
}

func traceBody(model Model, value DisplayItem) string {
	model.focusPane, model.detailRef = FocusInspector, &value
	model.buildDetailItems()
	body, _ := model.inspectorContent(100, true)
	return ansi.Strip(body)
}

func TestTraceOrderedInputHistoryAndUnresolvedWinner(t *testing.T) {
	report := traceFixtureReport()
	model := NewModel(report)
	value := traceFixtureValue(model)
	body := traceBody(model, value)
	for _, text := range []string{"INPUT HISTORY · LOW → HIGH", "shadowed", "effective inspected input", "Effective inspected input: false", "Final Helm result: incomplete"} {
		if !strings.Contains(body, text) {
			t.Fatalf("missing trace evidence %q:\n%s", text, body)
		}
	}
	if strings.Index(body, "defaults.yaml") > strings.Index(body, "operator.yaml") || !model.valueChanged(value) || model.valueUnresolved(value) {
		t.Fatal("source order or changed/unresolved classification is incorrect")
	}
	report.PackageValues[0].Sources = append(report.PackageValues[0].Sources, introspect.ValueSource{
		ID: "bundle:3", Kind: "bundle-values", Name: "computed.yaml", Status: introspect.ValueDynamic,
		Diagnostics: []introspect.ValueDiagnostic{{Status: introspect.ValueDynamic, Message: "Computed expressions are not evaluated."}},
	})
	model = NewModel(report)
	value = traceFixtureValue(model)
	body = traceBody(model, value)
	if !strings.Contains(body, "INCOMPLETE INPUT") || strings.Contains(body, "Effective inspected input:") || strings.Contains(body, "· effective inspected input") {
		t.Fatalf("unknown layer incorrectly received an effective winner:\n%s", body)
	}
	if !model.valueUnresolved(value) || value.authoring != nil {
		t.Fatal("incomplete history must be unresolved and must not offer last-known input as authoring")
	}
	table, err := model.valuePolicy.open(policyDeclarations(model.valuePolicy.catalog(), "core")[0])
	if err != nil || table.rows[0].status != "unresolved" || table.rows[0].authoring != nil {
		t.Fatal("chart mapping table must retain unresolved source evidence")
	}
}

func TestTraceExplicitNullAndFalseArePresent(t *testing.T) {
	for _, sample := range []struct {
		name  string
		value interface{}
		text  string
	}{{"null", nil, "null"}, {"false", false, "false"}, {"zero", 0, "0"}, {"empty", "", `""`}} {
		t.Run(sample.name, func(t *testing.T) {
			report := traceFixtureReport()
			report.PackageValues[0].Values = map[string]interface{}{"app": map[string]interface{}{"enabled": sample.value}}
			report.PackageValues[0].Sources[1].Values = report.PackageValues[0].Values
			model := NewModel(report)
			body := traceBody(model, traceFixtureValue(model))
			if !strings.Contains(body, "Effective inspected input: "+sample.text) || strings.Contains(body, "MISSING INPUT") {
				t.Fatalf("explicit %s became absent:\n%s", sample.name, body)
			}
		})
	}
}

func TestTraceMasksHistorySchemaAndAuthoringWithoutMutatingReport(t *testing.T) {
	report := traceFixtureReport()
	const sensitive = "fixture-private-value"
	report.PackageValues[0].Schema = map[string]interface{}{"properties": map[string]interface{}{
		"app": map[string]interface{}{"properties": map[string]interface{}{
			"enabled": map[string]interface{}{"writeOnly": true, "default": sensitive},
		}},
	}}
	report.PackageValues[0].Values = map[string]interface{}{"app": map[string]interface{}{"enabled": sensitive}}
	for i := range report.PackageValues[0].Sources {
		report.PackageValues[0].Sources[i].Values = report.PackageValues[0].Values
	}
	model := NewModel(report)
	value := traceFixtureValue(model)
	model.focusPane, model.detailRef = FocusInspector, &value
	model.detailBundleMode, model.detailSchemaShown = true, true
	model.buildDetailItems()
	body, _ := model.inspectorContent(110, true)
	if strings.Contains(body, sensitive) || !strings.Contains(body, redactedValue) {
		t.Fatal("sensitive schema input leaked through details/history/authoring")
	}
	if schema := formatYAMLValue(traceSafeSchema(report.PackageValues[0].Schema)); strings.Contains(schema, sensitive) {
		t.Fatal("schema default disclosed a writeOnly field")
	}
	if got := report.PackageValues[0].Values["app"].(map[string]interface{})["enabled"]; got != sensitive {
		t.Fatal("display redaction mutated immutable report input")
	}
	model = NewModel(introspect.InspectionReport{Variables: []v1alpha1.InteractiveVariable{{Variable: v1alpha1.Variable{Name: "OPAQUE", Sensitive: true}, Default: sensitive}}})
	legacy := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "OPAQUE", DefaultValue: sensitive}, IsPkgVar: true}
	if body := traceBody(model, legacy); strings.Contains(body, sensitive) {
		t.Fatal("explicit Sensitive legacy variable leaked")
	}
	nested := map[string]interface{}{"public": "visible", "auth": map[string]interface{}{"api_token": sensitive}}
	masked := traceSafeValue("", nested)
	if strings.Contains(formatYAMLValue(masked), sensitive) || !strings.Contains(formatYAMLValue(masked), "visible") {
		t.Fatal("recursive credential masking did not preserve public siblings")
	}
}

func TestTraceCacheSeparatesOwnersAndInspections(t *testing.T) {
	report := traceFixtureReport()
	other := report.PackageValues[0]
	other.PackageName = "other"
	other.Values = map[string]interface{}{"app": map[string]interface{}{"enabled": "other"}}
	other.Sources = []introspect.ValueSource{{ID: "packaged:1", Values: other.Values, Status: introspect.ValueResolved}}
	report.PackageValues = append(report.PackageValues, other)
	model := NewModel(report)
	for _, owner := range []string{"core", "other"} {
		value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "app.enabled"}, IsPackageValue: true, PackageName: owner}
		trace, ok := model.valueTrace(value)
		if !ok || owner == "core" && trace.Value != false || owner == "other" && trace.Value != "other" {
			t.Fatal("same path/source ID crossed package trace owners")
		}
	}
	if len(model.valuePolicy.tracers) != 2 {
		t.Fatal("expected a compiled immutable tracer per package")
	}
	newReport := traceFixtureReport()
	newReport.PackageValues[0].Sources[1].Values = map[string]interface{}{"app": map[string]interface{}{"enabled": true}}
	newModel := NewModel(newReport)
	trace, _ := newModel.valueTrace(traceFixtureValue(newModel))
	if trace.Value != true || trace.Changed {
		t.Fatal("a new inspection reused old cached history or ignored restoration")
	}
}

func TestTraceMaskSchemaCompositionReferencesAndParentSensitivity(t *testing.T) {
	const private = "fixture-hidden-content"
	for _, test := range []struct {
		name   string
		schema map[string]interface{}
	}{
		{"allOf", map[string]interface{}{"allOf": []interface{}{map[string]interface{}{"properties": map[string]interface{}{"payload": map[string]interface{}{"writeOnly": true}}}}}},
		{"local-ref", map[string]interface{}{"$defs": map[string]interface{}{"credential": map[string]interface{}{"writeOnly": true}}, "properties": map[string]interface{}{"payload": map[string]interface{}{"$ref": "#/$defs/credential"}}}},
		{"remote-ref", map[string]interface{}{"properties": map[string]interface{}{"payload": map[string]interface{}{"$ref": "https://example.invalid/private.json"}}}},
		{"cyclic-ref", map[string]interface{}{"$defs": map[string]interface{}{"loop": map[string]interface{}{"$ref": "#/$defs/loop"}}, "properties": map[string]interface{}{"payload": map[string]interface{}{"$ref": "#/$defs/loop"}}}},
		{"pattern", map[string]interface{}{"patternProperties": map[string]interface{}{"^pay": map[string]interface{}{"format": "password"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api", Schema: test.schema}}})
			value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "payload"}, PackageName: "api", IsPackageValue: true}
			if masked := model.displayValue(value, private); masked != redactedValue {
				t.Fatalf("%s sensitivity annotation was ignored", test.name)
			}
		})
	}
	parent := map[string]interface{}{"writeOnly": true, "properties": map[string]interface{}{
		"payload": map[string]interface{}{"type": "string", "default": private, "examples": []interface{}{private}},
	}}
	if strings.Contains(formatYAMLValue(traceSafeSchema(parent)), private) {
		t.Fatal("schema toggle disclosed a default/example under a sensitive parent")
	}
	composed := map[string]interface{}{
		"allOf":      []interface{}{map[string]interface{}{"properties": map[string]interface{}{"payload": map[string]interface{}{"writeOnly": true}}}},
		"properties": map[string]interface{}{"payload": map[string]interface{}{"default": private}},
		"default":    map[string]interface{}{"payload": private, "visible": "public"},
	}
	masked := formatYAMLValue(traceSafeSchema(composed))
	if strings.Contains(masked, private) || !strings.Contains(masked, "public") {
		t.Fatal("composed schema defaults must retain public siblings and hide private fields")
	}
}

func TestActualNextTraceDependenciesAndNestedReturn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := filepath.Abs("../../test/workspace/next")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := introspect.InspectNextUDSBundle(context.Background(), root, "", "amd64", introspect.IntrospectOptions{BundleConfig: filepath.Join(root, "config.uds.hcl")})
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(introspect.InspectionReport{Components: inspection.ChartResults, ComponentOrder: inspection.ComponentOrder,
		PackageValues: inspection.PackageValues, Bundle: inspection.Bundle, Variables: inspection.Variables})
	origin := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "scale", DefaultValue: inspection.Bundle.Variables["scale"]}, Component: "UDS Next bundle", IsBundleVariable: true, ValuePresent: true}
	model.focusPane, model.detailRef = FocusInspector, &origin
	model.buildDetailItems()
	findLink := func(match func(DisplayItem) bool) int {
		t.Helper()
		for i, detail := range model.detailItems {
			if detail.Kind == DetailItemTrace && detail.relatedValue != nil && match(*detail.relatedValue) {
				return i
			}
		}
		t.Fatal("observed typed trace link missing")
		return -1
	}
	firstCursor := findLink(func(value DisplayItem) bool { return value.IsPackageValue && value.PackageName == "api" })
	model.detailCursor, model.detailScrollOffset = firstCursor, 7
	model.activateFocused()
	if selected, _ := model.detailValue(); !selected.IsPackageValue || selected.PackageName != "api" {
		t.Fatal("bundle variable did not navigate to its actual consuming package")
	}
	packageView, _ := model.detailValue()
	body := traceBody(model, packageView)
	if !strings.Contains(body, "bundle .vars.scale") || !strings.Contains(body, "INCOMPLETE INPUT") || strings.Contains(body, "Effective inspected input:") {
		t.Fatalf("real Next references or unresolved later input lost:\n%s", body)
	}
	var originalSource bool
	for _, item := range model.detailItems {
		if item.Kind == DetailItemTrace && item.File == filepath.Join(root, "values.yaml") {
			originalSource = true
		}
	}
	if !originalSource {
		t.Fatal("source link must open original Next values file, not rendered cache")
	}
	secondCursor := findLink(func(value DisplayItem) bool { return value.IsBundleVariable && value.Path == "scale" })
	model.detailCursor, model.detailScrollOffset = secondCursor, 11
	model.activateFocused()
	for i, want := range []struct {
		cursor, offset int
		packageView    bool
	}{{secondCursor, 11, true}, {firstCursor, 7, false}} {
		updated, _ := model.Update(key("esc"))
		model = updated.(Model)
		selected, _ := model.detailValue()
		if selected.IsPackageValue != want.packageView || model.detailCursor != want.cursor || model.detailScrollOffset != want.offset || model.focusPane != FocusInspector {
			t.Fatalf("return %d lost typed origin or selection", i)
		}
	}
	if len(model.traceHistory) != 0 || !reflect.DeepEqual(*model.detailRef, origin) {
		t.Fatal("trace return stack did not restore its original value")
	}
}
