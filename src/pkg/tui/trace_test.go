package tui

import (
	"context"
	"encoding/json"
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
	model.detailPanel = detailProvenance
	model.buildDetailItems()
	body, _ := model.inspectorContent(100, true)
	return ansi.Strip(body)
}

func TestTraceOrderedInputHistoryAndUnresolvedWinner(t *testing.T) {
	report := traceFixtureReport()
	model := NewModel(report)
	value := traceFixtureValue(model)
	body := traceBody(model, value)
	for _, text := range []string{"Input history · low to high precedence", "shadowed", "effective inspected input", "Effective inspected input: false"} {
		if !strings.Contains(body, text) {
			t.Fatalf("missing trace evidence %q:\n%s", text, body)
		}
	}
	if strings.Index(body, "packaged defaults") > strings.Index(body, "operator values file") || !model.valueChanged(value) || model.valueUnresolved(value) {
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

func TestTraceRuntimeConsumersDoNotInventHelmDestinations(t *testing.T) {
	for _, test := range []struct {
		name       string
		usage      introspect.PackageValueUsage
		wantSource string
	}{
		{
			name: "action read",
			usage: introspect.PackageValueUsage{Path: ".app.enabled", ComponentName: "server", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "actions", Line: 4, Column: 8, Kind: "field"}}},
			wantSource: "component server · actions:4:8",
		},
		{
			name: "manifest named like chart",
			usage: introspect.PackageValueUsage{Path: ".app.enabled", ComponentName: "server", ChartName: "web", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "manifest.yaml", Line: 3, Kind: "field"}}},
			wantSource: "component server · manifest.yaml:3",
		},
		{
			name: "targetless observation",
			usage: introspect.PackageValueUsage{Path: ".app.enabled", ComponentName: "server", ChartName: "web",
				Locations: []introspect.ValueUsageLocation{{File: "template.yaml", Kind: "field"}}},
			wantSource: "component server · template.yaml",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := traceFixtureReport()
			report.PackageValues[0].Usages = []introspect.PackageValueUsage{test.usage}
			model := NewModel(report)
			sections := model.valueTraceSections(traceFixtureValue(model))
			var runtimeText string
			for _, section := range sections {
				if strings.Contains(section.title, "PACKAGE → HELM") {
					t.Fatal("runtime-only consumers received a Helm mapping heading")
				}
				for _, line := range section.lines {
					if strings.Contains(line.text, "Helm .Values") {
						t.Fatal("runtime observation invented a Helm target")
					}
					if section.title == "RUNTIME USES · NOT EVALUATED" {
						runtimeText += line.text + "\n"
					}
				}
			}
			for _, want := range []string{"Zarf .Values.app.enabled", test.wantSource, "runtime not evaluated"} {
				if !strings.Contains(runtimeText, want) {
					t.Fatalf("runtime trace omitted observed evidence %q: %s", want, runtimeText)
				}
			}
			// The same input may also have a real declared chart mapping.
			report.PackageValues[0].Usages = append(report.PackageValues[0].Usages,
				introspect.PackageValueUsage{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "web"})
			model = NewModel(report)
			var mapped, observed bool
			for _, section := range model.valueTraceSections(traceFixtureValue(model)) {
				observed = observed || section.title == "RUNTIME USES · NOT EVALUATED"
				if section.title == "TYPED BOUNDARY · PACKAGE → HELM" {
					for _, line := range section.lines {
						mapped = mapped || line.text == "Zarf .Values.app.enabled → server/web Helm .Values.config.enabled"
						if strings.Contains(line.text, test.wantSource) {
							t.Fatal("runtime source leaked into the declared mapping section")
						}
					}
				}
			}
			if !mapped || !observed {
				t.Fatal("mixed consumers must preserve both runtime evidence and the real Helm mapping")
			}
		})
	}
}

func TestTraceFixedOverridesChangedRequiresKnownDifferentBaseline(t *testing.T) {
	for _, test := range []struct {
		name            string
		original        interface{}
		override        interface{}
		chartBaseline   interface{}
		baselinePresent bool
		summary         bool
		changed         bool
	}{
		{name: "same known value", original: false, override: false},
		{name: "different known value", original: false, override: true, changed: true},
		{name: "absent baseline", override: true},
		{name: "same explicit null", baselinePresent: true},
		{name: "different from explicit null", override: false, baselinePresent: true, changed: true},
		{name: "known chart baseline", override: true, chartBaseline: false, baselinePresent: true, changed: true},
		{name: "summary explicit null", override: false, baselinePresent: true, summary: true, changed: true},
		{name: "same integer and float", original: int(1), override: float64(1)},
		{name: "same integer and JSON number", original: int(1), override: json.Number("1")},
		{name: "string differs from number", original: "1", override: float64(1), changed: true},
		{name: "nested numeric representations", original: map[string]interface{}{"ports": []interface{}{int(80), map[string]interface{}{"count": int64(1)}}},
			override: map[string]interface{}{"ports": []interface{}{float64(80), map[string]interface{}{"count": json.Number("1")}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{
				"core/server/web": {
					Values:            []introspect.ValueUsage{{Path: "enabled", DefaultValue: test.chartBaseline, DefaultPresent: test.baselinePresent}},
					UDSOverrideValues: []introspect.UDSOverrideValue{{Path: "enabled", Value: test.override, OriginalDefault: test.original}},
				},
			}, ComponentOrder: []string{"core/server/web"}}
			model := NewModel(report)
			value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "enabled"}, Component: "core/server/web",
				IsUDSOverrideValue: true, OriginalDefault: test.original, OverrideValue: test.override}
			if test.summary {
				value.Component, value.SummaryComponent, value.IsSummaryItem = "UDS Bundle Overrides", "core/server/web", true
			}
			if got := model.valueChanged(value); got != test.changed {
				t.Fatalf("changed = %t; want %t for fixed override with inspected baseline", got, test.changed)
			}
			var declarationVisible bool
			for _, row := range model.allValues {
				declarationVisible = declarationVisible || row.IsUDSOverrideValue && row.Path == "enabled"
			}
			if !declarationVisible {
				t.Fatal("override declaration must remain visible regardless of changed classification")
			}
		})
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
	model.openDetailPanel(detailDependencies, nil)
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
	packageView, _ := model.detailValue()
	if !packageView.IsPackageValue || packageView.PackageName != "api" {
		t.Fatal("bundle variable did not navigate to actual consuming package")
	}
	body := traceBody(model, packageView)
	if !strings.Contains(body, "INCOMPLETE INPUT") || strings.Contains(body, "Effective inspected input:") {
		t.Fatalf("unresolved layer lost: %s", body)
	}
	var originalSource bool
	for _, source := range model.inspectorSources(packageView) {
		if source.File == filepath.Join(root, "values.yaml") {
			originalSource = true
		}
	}
	if !originalSource {
		t.Fatal("source link must retain original template, not rendered cache")
	}
	model.openDetailPanel(detailDependencies, nil)
	secondCursor := findLink(func(value DisplayItem) bool { return value.IsBundleVariable && value.Path == "scale" })
	model.detailCursor, model.detailScrollOffset = secondCursor, 11
	model.activateFocused()
	updated, _ := model.Update(key("esc"))
	model = updated.(Model)
	selected, _ := model.detailValue()
	if !selected.IsPackageValue || model.detailPanel != detailDependencies || model.detailCursor != secondCursor || model.detailScrollOffset != 11 {
		t.Fatal("trace return lost package dependency context")
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	if model.detailPanel != detailSummary {
		t.Fatal("secondary Escape must return to package summary")
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(Model)
	if model.detailPanel != detailDependencies || model.detailCursor != firstCursor || model.detailScrollOffset != 7 {
		t.Fatal("outer trace return lost bundle dependency selection")
	}
	if len(model.traceHistory) != 0 || !reflect.DeepEqual(*model.detailRef, origin) {
		t.Fatal("trace return stack did not restore original value")
	}
}

func TestHelmSchemaMaskUsesRootReferencesAndAliasContext(t *testing.T) {
	const private = "private-fixture-value"
	root := map[string]interface{}{
		"$defs":      map[string]interface{}{"hidden": map[string]interface{}{"writeOnly": true}, "public": map[string]interface{}{"type": "string"}},
		"properties": map[string]interface{}{"access": map[string]interface{}{"$ref": "#/$defs/hidden", "default": private}, "label": map[string]interface{}{"$ref": "#/$defs/public"}},
	}
	model := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"api/server/web": {
		ValuesSchemas: []introspect.ChartValuesSchema{{Chart: "web", Schema: root},
			{Chart: "worker", ValuePrefix: "first", Schema: map[string]interface{}{"properties": map[string]interface{}{"name": map[string]interface{}{"writeOnly": true}}}},
			{Chart: "worker", ValuePrefix: "second", Schema: map[string]interface{}{"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}}},
		},
	}}})
	for _, sample := range []struct {
		path, ref string
		expected  interface{}
	}{
		{"access", "#/$defs/hidden", redactedValue}, {"label", "#/$defs/public", private}, {"first.name", "", redactedValue}, {"second.name", "", private},
	} {
		value := DisplayItem{Component: "api/server/web", ValueUsage: introspect.ValueUsage{Path: sample.path, DefaultValue: private, DefaultPresent: true}}
		if sample.ref != "" {
			value.Schema = map[string]interface{}{"$ref": sample.ref}
		}
		if got := model.displayValue(value, private); got != sample.expected {
			t.Fatalf("%s: got %v, want %v", sample.path, got, sample.expected)
		}
		if sample.path == "access" {
			model.focusPane, model.detailRef = FocusInspector, &value
			for _, panel := range []detailPanel{detailSummary, detailFullValue, detailSchema} {
				model.detailPanel = panel
				body, _ := model.inspectorContent(100, true)
				if strings.Contains(body, private) {
					t.Fatalf("schema-protected value leaked in panel %v", panel)
				}
			}
		}
	}
	value := DisplayItem{Component: "api/server/web", ValueUsage: introspect.ValueUsage{Path: "second.name", Chart: "worker"}}
	selected := model.selectedValueSchema(value).(map[string]interface{})
	if schemaSensitive(schemaForPath(selected, "name")) {
		t.Fatal("same chart name chose another alias's schema")
	}
}

func TestProjectedWrapperDefaultRetainsSchemaMaskInSummaryAndAuthoring(t *testing.T) {
	const private = "wrapper-private-fixture"
	for _, prefix := range []string{"", "alias"} {
		path := "access"
		if prefix != "" {
			path = prefix + "." + path
		}
		schema := map[string]interface{}{"properties": map[string]interface{}{"_internal_defaults_do_not_set": map[string]interface{}{"properties": map[string]interface{}{"access": map[string]interface{}{"writeOnly": true}}}}}
		model := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"core/server/web": {
			Values: []introspect.ValueUsage{{Path: path, DefaultValue: private, DefaultPresent: true}}, ValuesSchemas: []introspect.ChartValuesSchema{{Chart: "web", ValuePrefix: prefix, Schema: schema}},
		}}, PackageValues: []introspect.PackageValues{{PackageName: "core", Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".", ComponentName: "server", ChartName: "web"}}}}})
		value := DisplayItem{Component: "core/server/web", ValueUsage: introspect.ValueUsage{Path: path, DefaultValue: private, DefaultPresent: true}}
		model.focusPane, model.detailRef = FocusInspector, &value
		model.buildDetailItems()
		if got := model.displayValue(value, private); got != redactedValue {
			t.Fatalf("prefix %q exposed projected wrapper default", prefix)
		}
		for _, panel := range []detailPanel{detailSummary, detailFullValue, detailAuthoring} {
			model.openDetailPanel(panel, nil)
			body, _ := model.inspectorContent(100, true)
			if strings.Contains(body, private) {
				t.Fatalf("prefix %q panel %d leaked wrapper default", prefix, panel)
			}
		}
	}
}

func TestWrapperMaskDoesNotInventPrivateAdditionalProperty(t *testing.T) {
	schema := map[string]interface{}{"properties": map[string]interface{}{"label": map[string]interface{}{"type": "string"}}, "additionalProperties": map[string]interface{}{"writeOnly": true}}
	model := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"web": {ValuesSchemas: []introspect.ChartValuesSchema{{Schema: schema}}}}})
	value := DisplayItem{Component: "web", ValueUsage: introspect.ValueUsage{Path: "label"}}
	if got := model.displayValue(value, "visible"); got != "visible" {
		t.Fatal("invented wrapper path hid explicitly public value")
	}
}
