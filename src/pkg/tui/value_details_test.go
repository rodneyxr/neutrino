package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/goccy/go-yaml"
)

func TestPackageValuesAuthoringUsesNestedInputPath(t *testing.T) {
	const component = "istio-controlplane/base"
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{component: {
			Values: []introspect.ValueUsage{{Path: "base.excludedCRDs", DefaultValue: []interface{}{}}},
		}},
		ComponentOrder: []string{component},
		PackageValues: []introspect.PackageValues{{PackageName: "core-base",
			Usages: []introspect.PackageValueUsage{{Path: ".istio-controlplane.base", TargetPath: ".",
				ComponentName: "istio-controlplane", ChartName: "base"}},
		}},
	})
	model.currentChart = component
	model.updateFilteredValues()
	for i, value := range model.filteredValues {
		if value.Path == "base.excludedCRDs" {
			model.cursor = i
			break
		}
	}
	model.activateFocused()
	body, _ := model.inspectorContent(120, true)
	if strings.Contains(body, "PACKAGE VALUES AUTHORING") {
		t.Fatal("authoring must be opt-in")
	}
	updated, _ := model.Update(key("b"))
	model = updated.(Model)
	body, _ = model.inspectorContent(120, true)
	if !strings.Contains(body, "PACKAGE VALUES AUTHORING") {
		t.Fatalf("authoring toggle did not show Package Values snippet: %s", body)
	}
	value, _ := model.detailValue()
	snippet := model.packageValuesSnippet(value)
	var got map[string]interface{}
	if err := yaml.Unmarshal([]byte(snippet), &got); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, snippet)
	}
	want := map[string]interface{}{"istio-controlplane": map[string]interface{}{
		"base": map[string]interface{}{"base": map[string]interface{}{"excludedCRDs": []interface{}{}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incorrect Package Values nesting: %#v", got)
	}
}

func TestPackageValuesAuthoringPreservesSuppliedTypes(t *testing.T) {
	for _, input := range []interface{}{nil, false, 0, "", "true", []interface{}{}, map[string]interface{}{}, "line one\nline two"} {
		t.Run(fmt.Sprintf("%T_%v", input, input), func(t *testing.T) {
			model := Model{detailItems: []DetailItem{{packageMapping: &valueMappingDetail{
				row: mappingRow{packagePath: ".app.enabled", status: "available", present: true, input: input},
			}}}}
			snippet := model.packageValuesSnippet(DisplayItem{ValueUsage: introspect.ValueUsage{DefaultValue: "wrong-default"}})
			want := "# Current package input\n" + packageValuesYAML(".app.enabled", input)
			if snippet != want {
				t.Fatalf("supplied value replaced with default: %s", snippet)
			}
			var got map[string]interface{}
			if err := yaml.Unmarshal([]byte(snippet), &got); err != nil {
				t.Fatalf("invalid snippet YAML: %v", err)
			}
			actual := got["app"].(map[string]interface{})["enabled"]
			// Compare serialized values to tolerate the decoder's integer type.
			if packageValuesYAML("", actual) != packageValuesYAML("", input) {
				t.Fatalf("value changed during YAML round-trip: %#v -> %#v", input, actual)
			}
		})
	}
}

func TestPackageValuesAuthoringFollowsSelectedMapping(t *testing.T) {
	model := Model{detailItems: []DetailItem{
		{packageMapping: &valueMappingDetail{row: mappingRow{packagePath: ".first.enabled", status: "available"}}},
		{packageMapping: &valueMappingDetail{row: mappingRow{packagePath: ".second.enabled", status: "available"}}},
	}}
	model.detailCursor = 1
	snippet := model.packageValuesSnippet(DisplayItem{})
	if !strings.Contains(snippet, "second:") || strings.Contains(snippet, "first:") || !strings.Contains(snippet, "<set-value>") {
		t.Fatalf("wrong mapping or unknown default handling: %s", snippet)
	}
	for _, status := range []string{"excluded", "other", "runtime"} {
		model.detailItems[1].packageMapping.row.status = status
		if snippet := model.packageValuesSnippet(DisplayItem{}); snippet != "" {
			t.Fatalf("%s mapping should not offer a usable snippet: %s", status, snippet)
		}
	}
}

func TestPackageValueAuthoringPrecedesSchema(t *testing.T) {
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{},
		PackageValues: []introspect.PackageValues{{PackageName: "api",
			Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}},
			Schema: map[string]interface{}{"type": "object"},
		}},
	})
	model.activateFocused()
	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	body, _ := model.inspectorContent(120, true)
	authoring, schema := strings.Index(body, "PACKAGE VALUES AUTHORING"), strings.Index(body, "PACKAGE VALUES SCHEMA")
	if authoring < 0 || schema < authoring || !strings.Contains(body[authoring:schema], "enabled: false") {
		t.Fatalf("package input snippet missing or below schema: %s", body)
	}
	updated, _ = model.Update(key("b"))
	model = updated.(Model)
	body, _ = model.inspectorContent(120, true)
	if strings.Contains(body, "PACKAGE VALUES AUTHORING") {
		t.Fatal("second toggle should hide authoring")
	}
}
