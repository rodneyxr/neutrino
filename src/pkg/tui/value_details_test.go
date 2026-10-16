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
	model.selectSettingType(5)
	for i, value := range model.filteredValues {
		if value.Path == "base.excludedCRDs" {
			model.cursor = i
			break
		}
	}
	model.activateFocused()
	body, _ := model.inspectorContent(120, true)
	if strings.Contains(body, "Authoring example") {
		t.Fatal("authoring must be opt-in")
	}
	updated, _ := model.Update(key("b"))
	model = updated.(Model)
	body, _ = model.inspectorContent(120, true)
	if !strings.Contains(body, "Authoring example") {
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
	if !strings.Contains(body, "Authoring example") || !strings.Contains(body, "enabled: false") || strings.Contains(body, "type: object") {
		t.Fatalf("authoring should be separate from schema: %s", body)
	}
	updated, _ = model.Update(key("b"))
	model = updated.(Model)
	body, _ = model.inspectorContent(120, true)
	if strings.Contains(body, "Authoring example") {
		t.Fatal("second toggle should hide authoring")
	}
}

func TestAuthoringPanelRetainsSelectedAlternateMapping(t *testing.T) {
	model := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"core/server/web": {Values: []introspect.ValueUsage{{Path: "enabled", DefaultValue: true}}}},
		PackageValues: []introspect.PackageValues{{PackageName: "core", Usages: []introspect.PackageValueUsage{
			{Path: ".primary", TargetPath: ".", ComponentName: "server", ChartName: "web"},
			{Path: ".alternate", TargetPath: ".", ComponentName: "server", ChartName: "web"},
		}}},
	})
	value := DisplayItem{Component: "core/server/web", ValueUsage: introspect.ValueUsage{Path: "enabled", DefaultValue: true}}
	model.focusPane, model.detailRef = FocusInspector, &value
	model.buildDetailItems()
	model.detailCursor = 1
	model.openDetailPanel(detailAuthoring, nil)
	body, _ := model.inspectorContent(100, true)
	if !strings.Contains(body, "alternate:") || strings.Contains(body, "primary:") {
		t.Fatalf("authoring switched selected mapping: %s", body)
	}
	model.backDetailPanel()
	if model.detailCursor != 1 {
		t.Fatal("authoring lost primary mapping selection")
	}
}
