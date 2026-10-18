package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func settingsTypesModel() Model {
	return NewModel(introspect.InspectionReport{
		Bundle: &introspect.BundleMetadata{
			Format: "next", Name: "demo",
			Variables: map[string]interface{}{"replicas": 4},
			Packages:  []introspect.BundlePackageMetadata{{ID: "api"}, {ID: "api.extra"}},
		},
		ComponentOrder: []string{"api/server/app", "api.extra/server/app"},
		Components: map[string]*introspect.IntrospectResult{
			"api/server/app": {ChartName: "app", Values: []introspect.ValueUsage{
				{Path: "replicas", DefaultValue: 1}, {Path: "image.tag", DefaultValue: "stable"},
			}},
			"api.extra/server/app": {ChartName: "app", Values: []introspect.ValueUsage{
				{Path: "replicas", DefaultValue: 9},
			}},
		},
		PackageValues: []introspect.PackageValues{
			{PackageName: "api", Values: map[string]interface{}{"replicas": 2}},
			{PackageName: "api.extra", Values: map[string]interface{}{"replicas": 10}},
		},
	})
}

func typeSettingsText(m *Model, text string) {
	for _, r := range text {
		pressWorkspaceKey(m, string(r))
	}
}

func requireSettingsType(t *testing.T, m Model, category int) {
	t.Helper()
	if m.allCategories || m.category != category {
		t.Fatalf("want type %d, got category %d, All=%v", category, m.category, m.allCategories)
	}
	for _, item := range m.filteredValues {
		if got := nextCategory(item); got != category {
			t.Fatalf("type %d contains type %d row: %+v", category, got, item)
		}
	}
}

func TestSettingsTypeDefaultsFollowInspectedArtifact(t *testing.T) {
	chart := func() map[string]*introspect.IntrospectResult {
		return map[string]*introspect.IntrospectResult{
			"api/server/app": {ChartName: "app", Values: []introspect.ValueUsage{{Path: "replicas", DefaultValue: 1}}},
		}
	}
	for _, tc := range []struct {
		name     string
		report   introspect.InspectionReport
		category int
		next     bool
	}{
		{"next bundle variables", introspect.InspectionReport{
			Components: chart(),
			Bundle: &introspect.BundleMetadata{Format: "next", Variables: map[string]interface{}{"cluster": "dev"},
				Packages: []introspect.BundlePackageMetadata{{ID: "api", ValuesFiles: []string{"api.yaml"}}}},
		}, 0, true},
		{"next bundle files", introspect.InspectionReport{
			Components: chart(), Bundle: &introspect.BundleMetadata{Format: "next",
				Packages: []introspect.BundlePackageMetadata{{ID: "api", ValuesFiles: []string{"api.yaml"}}}},
		}, 1, true},
		{"legacy bundle overrides", introspect.InspectionReport{
			Components: chart(), Bundle: &introspect.BundleMetadata{Format: "legacy", Packages: []introspect.BundlePackageMetadata{{ID: "api"}}},
			UDSOverrideValues: []introspect.UDSOverrideValueWithContext{{
				UDSOverrideContext: introspect.UDSOverrideContext{PackageName: "api", ComponentName: "server", ChartName: "app"},
				UDSOverrideValue:   introspect.UDSOverrideValue{Path: "replicas", Value: 2},
			}},
		}, 6, false},
		{"direct Zarf Package Values", introspect.InspectionReport{
			Components: chart(), PackageAPIVersion: "zarf.dev/v1beta1",
			PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"replicas": 2}}},
		}, 3, false},
		{"direct Zarf variables", introspect.InspectionReport{
			PackageAPIVersion: "zarf.dev/v1alpha1",
			Components: map[string]*introspect.IntrospectResult{
				"server/app": {ChartName: "app", ZarfVars: []introspect.ZarfVariableUsage{{Name: "REPLICAS", DefaultValue: "2"}}},
			},
		}, 2, false},
		{"standalone Helm", introspect.InspectionReport{
			Components: map[string]*introspect.IntrospectResult{
				"": {ChartName: "app", Values: []introspect.ValueUsage{{Path: "replicas", DefaultValue: 1}}},
			},
		}, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(tc.report)
			requireSettingsType(t, m, tc.category)
			if len(m.filteredValues) == 0 || m.focusPane != FocusValues || m.nextRail != tc.next {
				t.Fatalf("default lost inputs, Settings focus, or format identity: %+v; focus %v, Next %v", m.filteredValues, m.focusPane, m.nextRail)
			}
			selected := fmt.Sprintf("%d %s %d", tc.category+1, settingTabLabels[tc.category], m.categoryCounts[tc.category])
			pane := m.valuesPane(84, 24)
			if !strings.Contains(pane, selected) || !strings.Contains(pane, "8 All") {
				t.Fatalf("Settings pane must render type tabs in every format; want %q:\n%s", selected, pane)
			}
			for _, shortcut := range []string{"1", "4", "6", "7"} {
				pressWorkspaceKey(&m, shortcut)
				requireSettingsType(t, m, int(shortcut[0]-'1'))
			}
			pressWorkspaceKey(&m, "8")
			if !m.allCategories || len(m.filteredValues) != len(m.allValues) {
				t.Fatal("All shortcut must work for every inspection format")
			}
		})
	}
}

func TestSettingsTypeSearchRetainsTypeAndQueryUntilExplicitAll(t *testing.T) {
	m := settingsTypesModel()
	resizeWorkspace(&m, 100, 30)
	pressWorkspaceKey(&m, "4")
	pressWorkspaceKey(&m, "l")
	pressWorkspaceKey(&m, "/")
	requireSettingsType(t, m, 3)
	if m.inputMode != InputModeFilter || m.focusPane != FocusValues {
		t.Fatal("/ did not open the Settings query editor")
	}
	typeSettingsText(&m, "replicas")
	requireSettingsType(t, m, 3)
	if len(m.filteredValues) != 2 {
		t.Fatalf("typed search must show only the two package inputs: %+v", m.filteredValues)
	}
	pressWorkspaceKey(&m, "enter")
	pressWorkspaceKey(&m, "/")
	if m.filterInput.Value() != "replicas" || m.previousFilter != "replicas" {
		t.Fatal("reopening search discarded the committed query")
	}
	typeSettingsText(&m, "-missing")
	if len(m.filteredValues) != 0 {
		t.Fatal("query with no match should leave the selected type empty")
	}
	requireSettingsType(t, m, 3)
	pressWorkspaceKey(&m, "esc")
	if m.inputMode != InputModeNormal || m.filterInput.Value() != "replicas" || len(m.filteredValues) != 2 {
		t.Fatal("canceling an edit did not restore the committed query and its results")
	}
	requireSettingsType(t, m, 3)
	pressWorkspaceKey(&m, "8")
	if !m.allCategories || m.filterInput.Value() != "replicas" || len(m.filteredValues) != 5 {
		t.Fatalf("explicit All must broaden the same query across types: %+v", m.filteredValues)
	}
	kinds := make(map[int]bool)
	for _, item := range m.filteredValues {
		kinds[nextCategory(item)] = true
		if item.Path != "replicas" {
			t.Fatalf("All discarded the active query: %+v", item)
		}
	}
	if !kinds[0] || !kinds[3] || !kinds[5] || len(kinds) != 3 {
		t.Fatalf("All did not expose bundle, package, and Helm matches: %v", kinds)
	}
	pressWorkspaceKey(&m, "/")
	if !m.allCategories || m.filterInput.Value() != "replicas" {
		t.Fatal("reopening search in All changed its type or query")
	}
	pressWorkspaceKey(&m, "esc")
	pressWorkspaceKey(&m, "6")
	requireSettingsType(t, m, 5)
	if len(m.filteredValues) != 2 || m.filterInput.Value() != "replicas" {
		t.Fatal("returning to Helm must narrow the same query")
	}
}

func TestSettingsTypeArrowsIncludeEmptyTypesAndRestoreSelection(t *testing.T) {
	m := NewModel(introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{
		"": {ChartName: "app", Values: []introspect.ValueUsage{{Path: "alpha"}, {Path: "beta"}}},
	}})
	resizeWorkspace(&m, 100, 24)
	pressWorkspaceKey(&m, "j")
	if m.cursor != 1 || m.filteredValues[m.cursor].Path != "beta" {
		t.Fatal("fixture did not select the second Helm setting")
	}
	for _, cycle := range []struct {
		key   string
		types []int
	}{
		{"right", []int{6, 7, 0, 1, 2, 3, 4, 5}},
		{"left", []int{4, 3, 2, 1, 0, 7, 6, 5}},
	} {
		for _, category := range cycle.types {
			pressWorkspaceKey(&m, cycle.key)
			if m.focusPane != FocusValues {
				t.Fatalf("%s moved focus out of Settings", cycle.key)
			}
			if category == 7 {
				if !m.allCategories || len(m.filteredValues) != 2 {
					t.Fatal("arrow cycle skipped All")
				}
				continue
			}
			requireSettingsType(t, m, category)
			if category != 5 && (len(m.filteredValues) != 0 || m.cursor != 0) {
				t.Fatalf("empty type %d should stay empty with a valid cursor", category)
			}
			if category == 5 && (m.cursor != 1 || m.filteredValues[m.cursor].Path != "beta") {
				t.Fatal("returning to Helm lost its selected setting")
			}
		}
	}
}

func TestSettingsTypeNavigationKeepsPaneAndScopeControlsDistinct(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := settingsTypesModel()
			resizeWorkspace(&m, width, 30)
			pressWorkspaceKey(&m, "6")
			for _, step := range []struct {
				key   string
				focus FocusPane
			}{{"l", FocusInspector}, {"h", FocusValues}, {"tab", FocusInspector}, {"shift+tab", FocusValues}} {
				pressWorkspaceKey(&m, step.key)
				if m.focusPane != step.focus {
					t.Fatalf("%s: focus %v, want %v", step.key, m.focusPane, step.focus)
				}
				requireSettingsType(t, m, 5)
			}
			pressWorkspaceKey(&m, "h")
			if width == 160 {
				if m.focusPane != FocusComponents {
					t.Fatal("h must reach Scope in the wide layout")
				}
				for i := 0; m.scopeTreeKey != "package:api" && i < len(m.scopes); i++ {
					pressWorkspaceKey(&m, "j")
				}
				if m.scopeTreeKey != "package:api" {
					t.Fatal("could not navigate to the API package in Scope")
				}
				pressWorkspaceKey(&m, "right")
				if !m.scopeExpanded["package:api"] || m.focusPane != FocusComponents {
					t.Fatal("Scope right arrow should expand the package")
				}
				pressWorkspaceKey(&m, "left")
				if m.scopeExpanded["package:api"] || m.currentChart != "All Charts" {
					t.Fatal("Scope left arrow should fold without applying a different scope")
				}
			} else if m.focusPane != FocusInspector {
				t.Fatal("h must cycle focus to Inspector in the medium layout")
			}
			requireSettingsType(t, m, 5)
			pressWorkspaceKey(&m, "l")
			if m.focusPane != FocusValues {
				t.Fatal("l should return focus to Settings")
			}
			requireSettingsType(t, m, 5)
		})
	}
}

func TestSettingsTypeAllSearchRetainsAppliedPackageScope(t *testing.T) {
	m := settingsTypesModel()
	resizeWorkspace(&m, 80, 30)
	pressWorkspaceKey(&m, "4")
	pressWorkspaceKey(&m, "/")
	typeSettingsText(&m, "replicas")
	pressWorkspaceKey(&m, "enter")
	for _, owner := range []string{"api", "api.extra"} {
		pressWorkspaceKey(&m, "f")
		if m.inputMode != InputModeScopeSelect {
			t.Fatal("f did not open the scope picker")
		}
		typeSettingsText(&m, "package:"+owner)
		pressWorkspaceKey(&m, "enter")
		if m.currentChart != "package:"+owner || m.filterInput.Value() != "replicas" {
			t.Fatal("applying package scope lost the selected owner or query")
		}
		if owner == "api" {
			requireSettingsType(t, m, 3)
			if len(m.filteredValues) != 1 || m.filteredValues[0].PackageName != owner {
				t.Fatalf("Package Values tab leaked the sibling package: %+v", m.filteredValues)
			}
		}
		pressWorkspaceKey(&m, "8")
		if !m.allCategories || len(m.filteredValues) != 2 || m.filterInput.Value() != "replicas" {
			t.Fatalf("scoped All should contain one package input and one Helm input: %+v", m.filteredValues)
		}
		packageInput, helm := false, false
		for _, item := range m.filteredValues {
			packageInput = packageInput || item.IsPackageValue && item.PackageName == owner
			helm = helm || !item.IsPackageValue && item.Component == owner+"/server/app"
			if item.IsBundleVariable || m.scopeItemOwner(item) != owner || item.Path != "replicas" {
				t.Fatalf("All escaped the package scope or query: %+v", item)
			}
		}
		if !packageInput || !helm {
			t.Fatal("All should expose both input types inside the selected package")
		}
		pressWorkspaceKey(&m, "6")
		requireSettingsType(t, m, 5)
		if len(m.filteredValues) != 1 || !strings.HasPrefix(m.filteredValues[0].Component, owner+"/") {
			t.Fatal("selecting Helm escaped package scope")
		}
		pressWorkspaceKey(&m, "1")
		pressWorkspaceKey(&m, "/")
		requireSettingsType(t, m, 0)
		if len(m.filteredValues) != 0 || m.filterInput.Value() != "replicas" {
			t.Fatal("searching an empty type should retain the scope and query")
		}
		pressWorkspaceKey(&m, "enter")
		pressWorkspaceKey(&m, "6")
	}
}

func TestEmptySettingTypeExplainsScopeAndAll(t *testing.T) {
	m := NewModel(introspect.InspectionReport{})
	m.selectSettingType(0)
	pane := m.valuesPane(64, 16)
	if !strings.Contains(pane, "No Bundle vars in this scope") || !strings.Contains(pane, "8 All types") {
		t.Fatalf("empty type must explain selected type and escape hatch: %s", pane)
	}
	m.filterInput.SetValue("missing")
	if pane = m.valuesPane(64, 16); !strings.Contains(pane, "No matching Bundle vars") {
		t.Fatalf("query miss must be distinguishable from empty scope: %s", pane)
	}
	m.selectSettingType(7)
	if pane = m.valuesPane(64, 16); strings.Contains(pane, "8 All types") {
		t.Fatal("All must not recommend selecting itself")
	}
	if settingsEntryCount(1) != "1 entry" || settingsEntryCount(2) != "2 entries" {
		t.Fatal("object group counts must use the correct plural")
	}
}
