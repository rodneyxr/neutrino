package tui

import (
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func configurationFixture() Model {
	return NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"api/server/web": {Values: []introspect.ValueUsage{
			{Path: "enabled", DefaultValue: true, DefaultPresent: true, Locations: []introspect.ValueUsageLocation{{File: "values.yaml", Line: 3, Kind: "value"}, {File: "templates/configmap.yaml", Line: 8, Kind: "field"}}},
			{Path: "port", DefaultValue: 80, DefaultPresent: true},
			{Path: "image", DefaultValue: "stable", DefaultPresent: true},
		}}},
		PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false, "port": 9090, "image": "edge"}}, Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".", ComponentName: "server", ChartName: "web", ExcludePaths: []string{".app.image"}}}}},
	})
}

func selectConfigurationPath(t *testing.T, m *Model, path string) DisplayItem {
	t.Helper()
	m.selectSettingType(3)
	for i, value := range m.filteredValues {
		if value.Path == path {
			m.cursor = i
			m.syncInspectorSelection()
			return value
		}
	}
	t.Fatalf("missing configuration %s", path)
	return DisplayItem{}
}

func TestConfigurationSummaryShowsComparisonAndFileCoordinatesBeforeRules(t *testing.T) {
	m := configurationFixture()
	resizeWorkspace(&m, 160, 40)
	selectConfigurationPath(t, &m, "app.enabled")
	m.activateFocused()
	body, _ := m.inspectorContent(76, true)
	for _, want := range []string{"app.enabled", "Underlying values", "Chart input", "true", "Setting input", "false", "Supplied to this chart path", "values.yaml:3", "templates/configmap.yaml:8", "View underlying values (1)", "Mapping explanation"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q: %s", want, body)
		}
	}
	if len(m.detailItems) == 0 || m.detailItems[0].Kind != DetailItemPanel || m.detailItems[0].panel != detailAffected {
		t.Fatal("first investigation must open underlying values, not mapping syntax")
	}
	m.activateFocused()
	if m.detailPanel != detailAffected || len(m.detailItems) != 1 || m.detailItems[0].Kind != DetailItemTrace {
		t.Fatal("underlying view must navigate actual chart settings")
	}
	m.activateFocused()
	value, _ := m.detailValue()
	if value.Component != "api/server/web" || value.Path != "enabled" {
		t.Fatalf("wrong underlying value: %+v", value)
	}
	pressWorkspaceKey(&m, "esc")
	if m.detailPanel != detailAffected {
		t.Fatal("back must restore underlying list")
	}
	pressWorkspaceKey(&m, "esc")
	if m.detailPanel != detailSummary {
		t.Fatal("back must restore configured input")
	}
}

func TestConfigurationImpactsKeepExclusionsEmptyInputsAndOccurrences(t *testing.T) {
	m := configurationFixture()
	group := selectConfigurationPath(t, &m, "app")
	rows := m.configurationImpacts(group)
	if len(rows) != 3 {
		t.Fatalf("want 3 chart candidates, got %d", len(rows))
	}
	foundExcluded := false
	for _, row := range rows {
		if row.path == "image" {
			foundExcluded = row.status == "excluded" && strings.Contains(row.state(), ".app.image")
		}
	}
	if !foundExcluded {
		t.Fatal("excluded candidate lost rule status")
	}
	report := m.report
	report.PackageValues[0].Values = map[string]interface{}{"app": map[string]interface{}{}}
	report.PackageValues[0].Usages = append(report.PackageValues[0].Usages, report.PackageValues[0].Usages[0])
	empty := NewModel(report)
	value := selectConfigurationPath(t, &empty, "app")
	rows = empty.configurationImpacts(value)
	if len(rows) != 6 {
		t.Fatalf("overlapping declaration occurrences collapsed: %d", len(rows))
	}
	for _, row := range rows {
		if row.present {
			t.Fatalf("empty object supplied chart field: %+v", row)
		}
	}
	if rows[0].mapping == rows[3].mapping {
		t.Fatal("declaration identities collapsed")
	}
}

func TestConfigurationPendingNullAndSensitiveInputsRemainHonest(t *testing.T) {
	m := configurationFixture()
	report := m.report
	report.PackageValues[0].Values = map[string]interface{}{"app": map[string]interface{}{"enabled": nil}}
	report.PackageValues[0].Sources = []introspect.ValueSource{{Kind: "bundle-values", Status: introspect.ValueDynamic}}
	m = NewModel(report)
	value := selectConfigurationPath(t, &m, "app.enabled")
	rows := m.configurationImpacts(value)
	if len(rows) != 1 || !rows[0].present || m.impactSettingInput(rows[0]) != "null" || rows[0].state() != "Pending deployment context" {
		t.Fatalf("pending null must remain known input, not absent/final: %+v", rows)
	}
	report.PackageValues[0].Values = map[string]interface{}{"app": map[string]interface{}{"password": "PRIVATE_INPUT"}}
	report.PackageValues[0].Usages = []introspect.PackageValueUsage{{Path: ".app.password", TargetPath: ".password", ComponentName: "server", ChartName: "web"}}
	report.Components["api/server/web"].Values = []introspect.ValueUsage{{Path: "password", DefaultValue: "PRIVATE_BASELINE", DefaultPresent: true}}
	m = NewModel(report)
	value = selectConfigurationPath(t, &m, "app.password")
	text := ""
	for _, row := range m.configurationImpacts(value) {
		text += m.impactChartInput(row) + m.impactSettingInput(row)
	}
	if strings.Contains(text, "PRIVATE_") || !strings.Contains(text, redactedValue) {
		t.Fatalf("comparison leaked credential-shaped values: %s", text)
	}
}

func TestConfigurationBundleReferencesDoNotInventChartLeaves(t *testing.T) {
	m := configurationFixture()
	bundle := DisplayItem{IsBundleVariable: true, ValueUsage: introspect.ValueUsage{Path: "scale", DefaultValue: 4}, ValuePresent: true}
	runtime := DisplayItem{IsPackageMapping: true, Runtime: true, ValueUsage: introspect.ValueUsage{Path: ".State.host"}}
	for _, value := range []DisplayItem{bundle, runtime} {
		if rows := m.configurationImpacts(value); len(rows) != 0 {
			t.Fatalf("fabricated leaf impact for dynamic/file-level reference: %+v", rows)
		}
	}
}

func TestConfigurationLegacyOverridesShowDestinationAndBaseline(t *testing.T) {
	m := configurationFixture()
	for _, value := range []DisplayItem{
		{Component: "api/server/web", IsUDSOverrideValue: true, ValueUsage: introspect.ValueUsage{Path: "enabled"}, OverrideValue: nil},
		{Component: "api/server/web", IsUDSOverrideVar: true, ValueUsage: introspect.ValueUsage{Path: "ENABLE_FEATURE", DefaultValue: false}, UDSVarPath: "enabled"},
	} {
		rows := m.configurationImpacts(value)
		if len(rows) != 1 || rows[0].path != "enabled" || m.impactChartInput(rows[0]) != "true" || !rows[0].present {
			t.Fatalf("legacy declaration lost comparison/explicit null: %+v", rows)
		}
	}
}

func TestConfigurationComparisonMasksDestinationSensitivity(t *testing.T) {
	for _, target := range []string{"password", "opaque"} {
		m := NewModel(introspect.InspectionReport{
			Components:    map[string]*introspect.IntrospectResult{"api/server/web": {Values: []introspect.ValueUsage{{Path: target, DefaultValue: "PRIVATE_BASELINE", Schema: map[string]interface{}{"writeOnly": true}}}}},
			PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"app": map[string]interface{}{"key": "PRIVATE_INPUT"}}, Usages: []introspect.PackageValueUsage{{Path: ".app.key", TargetPath: "." + target, ComponentName: "server", ChartName: "web"}}}},
		})
		value := selectConfigurationPath(t, &m, "app.key")
		rows := m.configurationImpacts(value)
		if len(rows) != 1 || strings.Contains(m.impactSettingInput(rows[0]), "PRIVATE_INPUT") || !strings.Contains(m.impactSettingInput(rows[0]), redactedValue) {
			t.Fatalf("destination %s sensitivity lost: %+v", target, rows)
		}
	}
}

func TestConfigurationEmptyRuleAndUnavailableSourceRemainInspectable(t *testing.T) {
	usage := introspect.PackageValueUsage{Path: ".missing", TargetPath: ".config", ComponentName: "server", ChartName: "web", ExcludePaths: []string{".missing.secret"}}
	report := introspect.InspectionReport{Components: map[string]*introspect.IntrospectResult{"api/server/web": {}}, PackageValues: []introspect.PackageValues{{PackageName: "api", Usages: []introspect.PackageValueUsage{usage}}}}
	m := NewModel(report)
	m.selectSettingType(4)
	value, _ := m.selectedValue()
	if len(m.configurationImpacts(value)) != 0 || len(m.configurationMappings(value)) != 1 {
		t.Fatal("empty chart rule must survive independently of comparison rows")
	}
	doc := m.configurationMappingDocument(value, inspectorDocument{})
	found := false
	for _, line := range doc.lines {
		found = found || line.text == ".missing.secret"
	}
	if !found || len(doc.lines) == 0 || doc.lines[len(doc.lines)-1].action == nil {
		t.Fatal("empty rule lost exclusions or browse action")
	}
	report.Components = nil
	report.PackageValues[0].Values = map[string]interface{}{"missing": map[string]interface{}{"enabled": false}}
	m = NewModel(report)
	m.selectSettingType(4)
	value, _ = m.selectedValue()
	rows := m.configurationImpacts(value)
	if len(rows) != 1 || !rows[0].present || rows[0].settingPath != "missing" || rows[0].status != "destination unavailable" {
		t.Fatalf("unavailable chart discarded source input: %+v", rows)
	}
	if input, ok := rows[0].input.(map[string]interface{}); !ok || input["enabled"] != false {
		t.Fatalf("wrong source supplied: %#v", rows[0].input)
	}
}

func TestConfigurationExplanationRetainsCopyableInputPath(t *testing.T) {
	m := configurationFixture()
	value := DisplayItem{Component: "api/server/web", ValueUsage: introspect.ValueUsage{Path: "enabled", DefaultValue: true}}
	m.focusPane, m.detailRef = FocusInspector, &value
	m.openDetailPanel(detailMappingRules, nil)
	if len(m.detailItems) != 1 || m.detailItems[0].packageMapping == nil {
		t.Fatal("explanation lost chart-side package path metadata")
	}
	updated, cmd := m.Update(key("p"))
	m = updated.(Model)
	if cmd == nil || m.statusMessage != "Copied .app.enabled" {
		t.Fatalf("mapping path shortcut regressed: %s", m.statusMessage)
	}
}

func TestUnavailableChartComparisonRetainsSelectedLeaf(t *testing.T) {
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"app": map[string]interface{}{"enabled": false}}, Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".config", ComponentName: "server", ChartName: "missing"}}}}})
	value := selectConfigurationPath(t, &m, "app.enabled")
	rows := m.configurationImpacts(value)
	if len(rows) != 1 || rows[0].settingPath != "app.enabled" || rows[0].path != "config.enabled" || rows[0].input != false || !rows[0].present {
		t.Fatalf("selected leaf lost identity: %+v", rows)
	}
}
