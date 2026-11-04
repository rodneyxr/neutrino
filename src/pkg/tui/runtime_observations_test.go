package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func TestPackageRuntimeConsumersKeepCoordinatesWithoutHelmTargets(t *testing.T) {
	dir := t.TempDir()
	uses := []introspect.PackageValueUsage{
		{Path: ".global.domain", ComponentName: "operator", ChartName: "config", Runtime: true,
			Locations: []introspect.ValueUsageLocation{{File: filepath.Join(dir, "operator", "config.yaml"), Line: 23, Kind: "field"}, {File: filepath.Join(dir, "operator", "config.yaml"), Line: 24, Kind: "field"}}},
		{Path: ".global.domain", ComponentName: "gateway", ChartName: "config", Runtime: true,
			Locations: []introspect.ValueUsageLocation{{File: filepath.Join(dir, "gateway", "config.yaml"), Line: 6, Kind: "field"}}},
		{Path: ".State.host", Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: "runtimeContext"}}},
		{Path: ".other", Runtime: true, Locations: []introspect.ValueUsageLocation{{Kind: "field"}}},
	}
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{
		{PackageName: "api", Values: map[string]interface{}{"global": map[string]interface{}{"domain": ""}}, Usages: uses},
		{PackageName: "worker", Values: map[string]interface{}{"global": map[string]interface{}{"domain": "other"}}, Usages: uses[:1]},
	}})
	v := selectConfigurationPath(t, &m, "global.domain")
	if v.PackageName != "api" {
		t.Fatalf("wrong test owner: %s", v.PackageName)
	}
	if len(m.observedValueUsages(v)) != 2 || len(m.configurationImpacts(v)) != 0 {
		t.Fatal("runtime observations were dropped or promoted to Helm destinations")
	}
	sources := m.inspectorSources(v)
	if len(sources) != 3 {
		t.Fatalf("recorded locations lost: %+v", sources)
	}
	for _, source := range sources {
		if source.File == "" || source.Location.Line <= 0 || strings.Contains(source.Label, dir) {
			t.Fatalf("missing coordinates or leaked absolute display root: %+v", source)
		}
	}
	doc := m.inspectorDocument(v)
	found := false
	for _, line := range doc.lines {
		found = found || line.action != nil && line.action.panel == detailReferences
	}
	if !found {
		t.Fatal("observed references have no navigable action")
	}
	m.detailRef = &v
	m.focusPane = FocusInspector
	m.openDetailPanel(detailReferences, nil)
	if len(m.detailItems) != 3 {
		t.Fatalf("template use view lost navigable coordinates: %+v", m.detailItems)
	}
	first := m.detailItems[0]
	m.activateFocused()
	if m.detailPanel != detailSourceFile || m.detailSource.File != first.File || m.detailSource.Location.Line != 23 {
		t.Fatal("short label changed the exact editor/source target")
	}
}

func TestRuntimeReferenceWithoutCoordinatesKeepsDiagnostic(t *testing.T) {
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{
		PackageName: "api", Values: map[string]interface{}{"host": "known"},
		Usages: []introspect.PackageValueUsage{{Path: ".host", Runtime: true, Diagnostic: "Template content unavailable", Status: introspect.ValueUnavailable}},
	}}})
	// The mapping row remains inspectable even when it has no location metadata.
	var selected DisplayItem
	for _, entry := range m.valuePolicy.catalog().packages {
		if entry.mapping != nil {
			selected = packageValueItem(entry, "Zarf Package Values")
			break
		}
	}
	doc := m.observedUsesDocument(selected, inspectorDocument{})
	var text string
	for _, line := range doc.lines {
		text += line.text + "\n"
	}
	if !strings.Contains(text, "Template content unavailable") || !strings.Contains(text, "Source location unavailable") {
		t.Fatalf("unknown coordinates hid the diagnostic: %s", text)
	}
}

func TestTemplateUseLabelsDisambiguateAcrossUsageGroups(t *testing.T) {
	dir := t.TempDir()
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api",
		Values: map[string]interface{}{"domain": "known"},
		Usages: []introspect.PackageValueUsage{
			{Path: ".domain", ComponentName: "server", ChartName: "web", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: filepath.Join(dir, "base", "config.yaml"), Line: 3, Kind: "field"}}},
			{Path: ".domain", ComponentName: "server", ChartName: "web", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: filepath.Join(dir, "env", "config.yaml"), Line: 3, Kind: "field"}}},
		},
	}}})
	v := selectConfigurationPath(t, &m, "domain")
	doc := m.observedUsesDocument(v, inspectorDocument{})
	var labels []string
	for _, line := range doc.lines {
		if line.action != nil {
			labels = append(labels, line.text)
		}
	}
	if len(labels) != 2 || labels[0] == labels[1] || !strings.Contains(labels[0], "base/config.yaml:3") || !strings.Contains(labels[1], "env/config.yaml:3") {
		t.Fatalf("separate usage groups hid distinct source identities: %v", labels)
	}
}

func TestTemplateUseIndexGroupsRepeatedConsumerLines(t *testing.T) {
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api",
		Values: map[string]interface{}{"global": map[string]interface{}{"domain": "known"}},
		Usages: []introspect.PackageValueUsage{
			{Path: ".global", ComponentName: "server", ChartName: "web", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "templates/config.yaml", Line: 8, Column: 5, Kind: "field"}}},
			{Path: ".global.domain", ComponentName: "server", ChartName: "web", Runtime: true,
				Locations: []introspect.ValueUsageLocation{{File: "templates/config.yaml", Line: 8, Column: 12, Kind: "field"}}},
		},
	}}})
	v := selectConfigurationPath(t, &m, "global.domain")
	doc := m.observedUsesDocument(v, inspectorDocument{})
	var actions, headings int
	for _, line := range doc.lines {
		if line.action != nil {
			actions++
		}
		if line.section {
			headings++
		}
	}
	if actions != 1 || headings != 1 || len(m.inspectorSources(v)) != 2 {
		t.Fatalf("line index repeats consumers or loses full source occurrences: actions=%d headings=%d", actions, headings)
	}
}

func TestFileShapedRuntimeReferencesUseShortDisplayIdentity(t *testing.T) {
	file := filepath.Join(t.TempDir(), "templates", "invalid.txt")
	m := NewModel(introspect.InspectionReport{PackageValues: []introspect.PackageValues{{PackageName: "api", Usages: []introspect.PackageValueUsage{{
		Path: file, Runtime: true, Status: introspect.ValueParseError,
		Locations: []introspect.ValueUsageLocation{{File: file, Kind: "unparsedTemplate"}},
	}}}}})
	var selected DisplayItem
	for _, item := range m.allValues {
		if item.Path == file {
			selected = item
			break
		}
	}
	if got := m.valueListPath(selected); got != "invalid.txt" {
		t.Fatalf("runtime row exposes resolver filesystem path: %q", got)
	}
	if got := m.inspectorDocument(selected).title; got != "invalid.txt" || selected.Path != file {
		t.Fatalf("title changed raw identity or failed to shorten: title=%q raw=%q", got, selected.Path)
	}
	for _, reason := range m.configurationProblems(selected) {
		if strings.Contains(reason, filepath.Dir(file)) {
			t.Fatalf("diagnostic exposes resolver root: %s", reason)
		}
	}
}

func TestInspectorSourcesKeepRelativeCoordinatesFromDifferentOwners(t *testing.T) {
	m := NewModel(introspect.InspectionReport{})
	v := DisplayItem{IsZarfVar: true, ZarfReferences: []zarfVariableReference{
		{component: "api/server/web", locations: []introspect.ValueUsageLocation{{File: "values.yaml", Line: 8, Kind: "value"}}},
		{component: "worker/server/web", locations: []introspect.ValueUsageLocation{{File: "values.yaml", Line: 8, Kind: "value"}}},
	}}
	sources := m.inspectorSources(v)
	if len(sources) != 2 || sources[0].Label == sources[1].Label {
		t.Fatalf("different owners' relative sources collapsed: %+v", sources)
	}
	for _, source := range sources {
		if source.File != "values.yaml" || source.Location.Line != 8 {
			t.Fatalf("display disambiguation changed recorded identity: %+v", source)
		}
	}
}

func TestCompactSummaryDoesNotRepeatLeafInputOrCounts(t *testing.T) {
	m := configurationFixture()
	v := selectConfigurationPath(t, &m, "app.enabled")
	doc := m.inspectorDocument(v)
	var inputCount, sourcesCount int
	for _, line := range doc.lines {
		if line.text == "false" {
			inputCount++
		}
		if line.label == "Setting" || line.label == "Setting input" || line.label == "Compared paths" || line.label == "Origin" {
			t.Fatalf("redundant leaf summary field: %+v", line)
		}
		if strings.HasPrefix(line.text, "Sources (") {
			sourcesCount++
		}
	}
	if inputCount != 1 || sourcesCount != 1 {
		t.Fatalf("summary repeats input or source navigation: input=%d sources=%d", inputCount, sourcesCount)
	}
}

func TestNarrowInspectorRetainsCompleteDestinationAndHistory(t *testing.T) {
	m := NewModel(introspect.InspectionReport{})
	for _, width := range []int{40, 80, 99, 100, 120, 160} {
		resizeWorkspace(&m, width, 24)
		m.focusPane = FocusInspector
		m.detailRef = &DisplayItem{ValueUsage: introspect.ValueUsage{Path: "CPU_REQUEST", DefaultValue: "100m"}, IsUDSOverrideVar: true,
			Component: "core-base/pepr-uds-core/module", UDSVarPath: "admission.resources.requests.cpu"}
		m.detailPanel = detailAffected
		m.buildDetailItems()
		if width < 100 && m.inspectorWidth() != width {
			t.Fatalf("focused narrow Inspector still splits width %d", width)
		}
		content, _ := m.inspectorContent(m.inspectorWidth()-4, true)
		compact := strings.ReplaceAll(content, "\n", "")
		if !strings.Contains(strings.ReplaceAll(compact, " ", ""), "admission.resources.requests.cpu") {
			t.Fatalf("destination path is hidden at width %d: %s", width, content)
		}
		assertWorkspaceSize(t, m, width, 24)
	}
	if got := wrapProse("Underlying chart values · inspected inputs", 40); strings.Contains(got, "inp\nuts") {
		t.Fatalf("prose split a short word: %s", got)
	}
}

func TestShortInspectorCanScrollPastFinalActionToItsEffect(t *testing.T) {
	m := NewModel(introspect.InspectionReport{})
	resizeWorkspace(&m, 40, 12)
	m.focusPane = FocusInspector
	m.detailRef = &DisplayItem{ValueUsage: introspect.ValueUsage{Path: "CPU_REQUEST", DefaultValue: "100m"}, IsUDSOverrideVar: true,
		Component: "core-base/pepr-uds-core/module", UDSVarPath: "admission.resources.requests.cpu"}
	m.detailPanel = detailAffected
	m.buildDetailItems()
	for i := 0; i < 60; i++ {
		m.moveWithinFocusedPane(1)
	}
	if m.detailScrollOffset != m.detailScrollLimit() || !strings.Contains(m.View(), "override") {
		t.Fatalf("final evidence remains unreachable: offset=%d limit=%d\n%s", m.detailScrollOffset, m.detailScrollLimit(), m.View())
	}
}
