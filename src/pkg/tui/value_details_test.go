package tui

import (
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
