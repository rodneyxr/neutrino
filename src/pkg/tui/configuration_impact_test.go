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

func TestConfigurationSummaryLeadsToUnderlyingValuesAndRecordedSources(t *testing.T) {
	m := configurationFixture()
	resizeWorkspace(&m, 160, 40)
	selectConfigurationPath(t, &m, "app.enabled")
	m.activateFocused()
	body, _ := m.inspectorContent(76, true)
	for _, want := range []string{"app.enabled", "Underlying values", "Chart input", "true", "false", "Supplied to this chart path", "View underlying values (1)", "Mapping explanation", "Sources (2)"} {
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
	body, _ = m.inspectorContent(76, true)
	if !strings.Contains(body, "Setting input") || !strings.Contains(body, "false") {
		t.Fatal("detailed comparison lost supplied input")
	}
	sources := m.inspectorSources(m.filteredValues[m.cursor])
	if len(sources) != 2 || sources[0].File != "values.yaml" || sources[0].Location.Line != 3 ||
		sources[1].File != "templates/configmap.yaml" || sources[1].Location.Line != 8 ||
		!strings.Contains(sources[0].Label, "values.yaml:3") || !strings.Contains(sources[1].Label, "configmap.yaml:8") {
		t.Fatalf("recorded source coordinates lost: %+v", sources)
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

func TestConfigurationDiagnosticKindsRemainActionable(t *testing.T) {
	for _, tc := range []struct {
		status introspect.ValueStatus
		state  string
	}{
		{introspect.ValueMissing, "Missing configuration input"},
		{introspect.ValueParseError, "Input could not be parsed"},
		{introspect.ValueUnavailable, "Input unavailable for inspection"},
		{introspect.ValueDynamic, "Pending deployment context"},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			report := configurationFixture().report
			report.PackageValues[0].Usages[0].Status = tc.status
			report.PackageValues[0].Usages[0].Diagnostic = "Recorded inspection reason"
			if tc.status == introspect.ValueMissing {
				report.PackageValues[0].Values = nil
			}
			m := NewModel(report)
			value := DisplayItem{Component: "api/server/web", ValueUsage: report.Components["api/server/web"].Values[0]}
			rows := m.configurationImpacts(value)
			if len(rows) != 1 || rows[0].state() != tc.state {
				t.Fatalf("wrong diagnostic state: %+v", rows)
			}
			if tc.status == introspect.ValueMissing && (rows[0].present || m.impactSettingInput(rows[0]) != "Not supplied") {
				t.Fatal("missing source became a supplied input")
			}
			checkReason := func(lines []inspectorLine) {
				t.Helper()
				for _, line := range lines {
					if line.label == "Reason" && strings.Contains(line.text, "Recorded inspection reason") {
						return
					}
				}
				t.Fatal("recorded diagnostic is inaccessible")
			}
			checkReason(m.impactLines(rows, 0, true))
			checkReason(m.configurationMappingDocument(value, inspectorDocument{}).lines)
			excluded := rows[0]
			excluded.status, excluded.exclusion = "excluded", ".app.enabled"
			if excluded.state() != "Excluded by .app.enabled" {
				t.Fatal("source diagnostic erased the exclusion")
			}
		})
	}
}

func TestConfigurationProblemsIncludeSourcesWithoutStaticMappings(t *testing.T) {
	report := introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api",
		Values: map[string]interface{}{"enabled": true},
		Sources: []introspect.ValueSource{{Status: introspect.ValueParseError,
			Diagnostics: []introspect.ValueDiagnostic{{Status: introspect.ValueParseError, Message: "Invalid values file"}}}},
		Usages: []introspect.PackageValueUsage{{Path: ".enabled", Runtime: true, Status: introspect.ValueDynamic,
			Diagnostic: "Observed template requires evaluation", Locations: []introspect.ValueUsageLocation{{Kind: "field"}}}},
	}}}
	m := NewModel(report)
	value := selectConfigurationPath(t, &m, "enabled")
	if len(m.configurationMappings(value)) != 0 {
		t.Fatal("runtime observation acquired a static chart mapping")
	}
	reasons := strings.Join(m.configurationProblems(value), "\n")
	if !strings.Contains(reasons, "Invalid values file") || !strings.Contains(reasons, "Observed template requires evaluation") {
		t.Fatalf("source/runtime reason lost without static mappings: %s", reasons)
	}
	if !strings.HasPrefix(reasons, "parse error:") {
		t.Fatalf("fixable error hidden behind runtime evaluation: %s", reasons)
	}
}

func TestConfigurationPreviewsKeepSettingInputSeparateFromChartBaseline(t *testing.T) {
	setting, baseline := strings.Repeat("setting-", 12), strings.Repeat("baseline-", 12)
	m := NewModel(introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"api/server/web": {
			Values: []introspect.ValueUsage{{Path: "message", DefaultValue: baseline, DefaultPresent: true}},
		}},
		PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"app": map[string]interface{}{"message": setting}},
			Usages: []introspect.PackageValueUsage{{Path: ".app", TargetPath: ".", ComponentName: "server", ChartName: "web"}},
		}},
	})
	value := selectConfigurationPath(t, &m, "app")
	rows := m.configurationImpacts(value)
	lines := m.impactLines(rows, 0, true)
	if len(lines) == 0 || lines[0].action == nil || lines[0].action.Kind != DetailItemTrace {
		t.Fatal("chart navigation must remain the first action")
	}
	want := map[string]interface{}{"View full setting input": setting, "View full chart input": baseline}
	for _, line := range lines {
		if strings.Contains(line.text, "open chart value") {
			t.Fatal("comparison advertises an ambiguous chart-value action")
		}
		if expected, ok := want[line.text]; ok {
			if line.action == nil || line.action.Kind != DetailItemPreview || line.action.preview == nil || line.action.preview.value != expected {
				t.Fatalf("preview opens wrong value: %+v", line)
			}
			delete(want, line.text)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing independently selectable previews: %v", want)
	}
	m.focusPane, m.detailRef = FocusInspector, &value
	m.openDetailPanel(detailAffected, nil)
	for _, expected := range []string{setting, baseline} {
		found := false
		for i, item := range m.detailItems {
			if item.Kind == DetailItemPreview && item.preview.value == expected {
				m.detailCursor = i
				found = true
				break
			}
		}
		if !found {
			t.Fatal("preview missing from navigable inspector actions")
		}
		m.activateFocused()
		body, _ := m.inspectorContent(70, true)
		body = strings.Join(strings.Fields(body), "")
		if !strings.Contains(body, expected) {
			t.Fatalf("preview navigation showed another input: %s", body)
		}
		pressWorkspaceKey(&m, "esc")
		if m.detailPanel != detailAffected {
			t.Fatal("preview did not restore comparison navigation")
		}
	}
}

func TestConfigurationPreviewsStoreOnlyMaskedValues(t *testing.T) {
	input := map[string]interface{}{"public": strings.Repeat("visible", 12), "sourceHidden": "SOURCE_PRIVATE", "destinationHidden": "DESTINATION_PRIVATE"}
	before := formatYAMLValue(input)
	property := func(name string) map[string]interface{} {
		return map[string]interface{}{"properties": map[string]interface{}{name: map[string]interface{}{"writeOnly": true}}}
	}
	report := introspect.InspectionReport{
		Components: map[string]*introspect.IntrospectResult{"api/server/web": {Values: []introspect.ValueUsage{
			{Path: "payload", DefaultValue: input, DefaultPresent: true, Schema: property("destinationHidden")},
		}}},
		PackageValues: []introspect.PackageValues{{PackageName: "api", Values: map[string]interface{}{"payload": input},
			Schema: map[string]interface{}{"properties": map[string]interface{}{"payload": property("sourceHidden")}},
			Usages: []introspect.PackageValueUsage{{Path: ".payload", TargetPath: ".payload", ComponentName: "server", ChartName: "web"}},
		}},
	}
	m := NewModel(report)
	value := selectConfigurationPath(t, &m, "payload")
	previews := 0
	var rows []configurationImpact
	for _, row := range m.configurationImpacts(value) {
		if row.path == "payload" {
			rows = append(rows, row)
		}
	}
	for _, line := range m.impactLines(rows, 0, true) {
		if line.action == nil || line.action.Kind != DetailItemPreview {
			continue
		}
		previews++
		text := formatYAMLValue(line.action.preview.value)
		if strings.Contains(text, "PRIVATE") || !strings.Contains(text, "visible") || !strings.Contains(text, redactedValue) {
			t.Fatalf("preview lost masking or public input: %s", text)
		}
	}
	if previews < 2 || before != formatYAMLValue(input) {
		t.Fatal("preview missing or borrowed inspection input mutated")
	}
}

func TestConfigurationProblemsPrioritizeFixableUsageErrors(t *testing.T) {
	report := configurationFixture().report
	report.PackageValues[0].Sources = []introspect.ValueSource{{Status: introspect.ValueDynamic}}
	report.PackageValues[0].Usages[0].Status = introspect.ValueParseError
	report.PackageValues[0].Usages[0].Diagnostic = "Fix the malformed configuration"
	m := NewModel(report)
	value := selectConfigurationPath(t, &m, "app.enabled")
	reasons := m.configurationProblems(value)
	if len(reasons) < 2 || !strings.Contains(reasons[0], "Fix the malformed configuration") {
		t.Fatalf("runtime source obscured actionable error: %v", reasons)
	}
}

func TestConfigurationProblemsRetainDistinctMissingVariablesAndFiles(t *testing.T) {
	sources := []introspect.ValueSource{
		{Name: "/project/base/values.yaml", Kind: "bundle-values", Status: introspect.ValueMissing, Diagnostics: []introspect.ValueDiagnostic{
			{Status: introspect.ValueMissing, Path: ".domain", Message: "Bundle variable is absent from inspected defaults and configuration."},
			{Status: introspect.ValueMissing, Path: ".region", Message: "Bundle variable is absent from inspected defaults and configuration."},
		}},
		{Name: "/project/env/values.yaml", Kind: "bundle-values", Status: introspect.ValueMissing, Diagnostics: []introspect.ValueDiagnostic{
			{Status: introspect.ValueMissing, Path: ".domain", Message: "Bundle variable is absent from inspected defaults and configuration."},
		}},
	}
	reasons := configurationDiagnostics(nil, sources)
	if len(reasons) != 3 {
		t.Fatalf("distinct missing variables/files collapsed: %v", reasons)
	}
	for i, want := range []string{"base/values.yaml · .domain", "base/values.yaml · .region", "env/values.yaml · .domain"} {
		if !strings.Contains(reasons[i], want) || strings.Contains(reasons[i], "/project/") {
			t.Fatalf("diagnostic lost actionable context or used an absolute display path: %v", reasons)
		}
	}
}

func TestUnavailableAndUnparsedTemplateEvidenceIsNotRuntimeWait(t *testing.T) {
	for _, sample := range []struct {
		kind   string
		status introspect.ValueStatus
		reason string
	}{
		{"unavailableTemplate", introspect.ValueUnavailable, "unavailable input"},
		{"unparsedTemplate", introspect.ValueParseError, "parse error"},
	} {
		usage := introspect.PackageValueUsage{Path: ".", Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: sample.kind}}}
		if got := configurationUsageStatus(usage); got != sample.status {
			t.Fatalf("%s became %s", sample.kind, got)
		}
		reasons := strings.Join(configurationDiagnostics(&usage, nil), "\n")
		if !strings.Contains(reasons, sample.reason) || strings.Contains(reasons, "Needs evaluation") {
			t.Fatalf("actionable template evidence became a runtime wait: %s", reasons)
		}
	}
}

func TestConfigurationProblemsConsolidateEquivalentRuntimeCauses(t *testing.T) {
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api",
		Values: map[string]interface{}{"global": map[string]interface{}{"domain": "known"}}, Usages: []introspect.PackageValueUsage{
			{Path: ".global", Runtime: true, Diagnostic: "Package reference requires runtime evaluation.", Locations: []introspect.ValueUsageLocation{{Kind: "field"}}},
			{Path: ".global.domain", Runtime: true, Diagnostic: "Package reference requires runtime evaluation.", Locations: []introspect.ValueUsageLocation{{Kind: "field"}}},
		},
	}}})
	v := selectConfigurationPath(t, &m, "global.domain")
	if reasons := m.configurationProblems(v); len(reasons) != 1 {
		t.Fatalf("equivalent parent and leaf runtime causes repeat: %v", reasons)
	}
}
