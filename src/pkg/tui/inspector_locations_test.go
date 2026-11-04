package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func TestSourceCoordinateLabelUsesRecordedFileAndLine(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		line       int
		want       string
	}{
		{"template", "templates/deployment.yaml", 37, "templates/deployment.yaml:37"},
		{"absolute source", "/work/packages/api/values.yaml", 12, "/work/packages/api/values.yaml:12"},
		{"relative identity", "../inputs/values.yaml", 4, "../inputs/values.yaml:4"},
		{"Unicode identity", "配置/値.yaml", 6, "配置/値.yaml:6"},
		{"Windows identity", `C:\packages\api\values.yaml`, 3, `C:\packages\api\values.yaml:3`},
		{"unknown line", "values.yaml", 0, "values.yaml · line unavailable"},
		{"negative line", "values.yaml", -8, "values.yaml · line unavailable"},
		{"unknown file", "", 37, "File unavailable · line 37"},
		{"unknown coordinates", "", 0, "File unavailable · line unavailable"},
		{"whitespace only", " \t\r\n", 0, "File unavailable · line unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceCoordinateLabel(tc.file, tc.line); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInspectorSourceLabelsDisambiguateDuplicateBasenames(t *testing.T) {
	sources := []DetailItem{
		{Kind: DetailItemLocation, Location: introspect.ValueUsageLocation{File: "api/templates/deployment.yaml", Line: 12, Kind: "field"}},
		{Kind: DetailItemLocation, Location: introspect.ValueUsageLocation{File: "worker/templates/deployment.yaml", Line: 12, Kind: "field"}},
	}
	labels := inspectorSourcePreviews(sources, 2)
	if len(labels) != 2 || labels[0] == labels[1] {
		t.Fatalf("same-basename sources became indistinguishable: %q", labels)
	}
	for i, source := range sources {
		if !strings.HasPrefix(labels[i], source.Location.File+":12 · ") {
			t.Fatalf("label lost its complete recorded identity: %q", labels[i])
		}
	}
}

func TestInspectorSourceLabelKeepsRecordedRoleAndCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source DetailItem
		want   string
	}{
		{"template role", DetailItem{Kind: DetailItemLocation,
			Location: introspect.ValueUsageLocation{File: "templates/deployment.yaml", Line: 37, Kind: "field"}},
			"templates/deployment.yaml:37 · Template"},
		{"chart defaults", DetailItem{Kind: DetailItemLocation,
			Location: introspect.ValueUsageLocation{File: "charts/app/values.yaml", Line: 8, Kind: "value"}},
			"charts/app/values.yaml:8 · Chart value declaration"},
		{"runtime values template", DetailItem{Kind: DetailItemSource, File: "values/runtime.yaml",
			Location: introspect.ValueUsageLocation{File: "values/runtime.yaml", Line: 17, Kind: "runtimeContext"}},
			"values/runtime.yaml:17 · Chart values template"},
		{"owning chart", DetailItem{Kind: DetailItemLocation, sourceComponent: "api/server/app",
			Location: introspect.ValueUsageLocation{File: "templates/config.yaml", Line: 5}},
			"templates/config.yaml:5 · server/app · Template"},
		{"friendly package role", DetailItem{Kind: DetailItemSource, File: "operator/api.yaml", Label: "api / operator values file 2"},
			"operator/api.yaml · line unavailable · api / operator values file 2"},
		{"absent file with role", DetailItem{Kind: DetailItemSource, Label: "Bundle values template", Location: introspect.ValueUsageLocation{Line: 9}},
			"File unavailable · line 9 · Bundle values template"},
		{"absent metadata", DetailItem{Kind: DetailItemSource},
			"File unavailable · line unavailable · Values source"},
		{"different target file", DetailItem{Kind: DetailItemSource, File: "operator.yaml",
			Location: introspect.ValueUsageLocation{File: "templates/deployment.yaml", Line: 42}},
			"operator.yaml · line unavailable · Values source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectorSourceLabel(tc.source); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			tc.source.Label = tc.want
			if got := inspectorSourceLabel(tc.source); got != tc.want {
				t.Fatalf("already labeled source gained duplicate coordinates: %q", got)
			}
		})
	}
}

func TestInspectorSourceLabelsSanitizeFileAndRole(t *testing.T) {
	source := DetailItem{Kind: DetailItemSource,
		File:     "values/\x1b[31mapi\x1b[0m.yaml\r\n\t\x00",
		Label:    "operator\x1b]52;c;payload\a values\rfile",
		Location: introspect.ValueUsageLocation{Line: 8},
	}
	label := inspectorSourceLabel(source)
	for _, forbidden := range []string{"\x1b", "\a", "\r", "\n", "\t", "\x00", "payload"} {
		if strings.Contains(label, forbidden) {
			t.Fatalf("terminal control content survived in source label: %q", label)
		}
	}
	if !strings.HasPrefix(label, "values/api.yaml") || !strings.Contains(label, ":8 · operator") {
		t.Fatalf("sanitizing source label lost filename, line, or role: %q", label)
	}
}

func TestInspectorSourcePreviewsPreserveOrderAndInput(t *testing.T) {
	sources := []DetailItem{
		{Kind: DetailItemSource, File: "packaged.yaml", Label: "packaged defaults"},
		{Kind: DetailItemSource, File: "bundle.yaml", Label: "bundle input"},
		{Kind: DetailItemSource, File: "operator.yaml", Label: "operator input"},
	}
	before := append([]DetailItem(nil), sources...)
	previews := inspectorSourcePreviews(sources, 2)
	if len(previews) != 2 || !strings.HasPrefix(previews[0], "packaged.yaml") || !strings.HasPrefix(previews[1], "bundle.yaml") {
		t.Fatalf("preview limit or input ordering changed: %q", previews)
	}
	if !reflect.DeepEqual(sources, before) {
		t.Fatal("preview generation mutated navigable source targets")
	}
	if len(inspectorSourcePreviews(sources, 100)) != 3 || inspectorSourcePreviews(sources, 0) != nil || inspectorSourcePreviews(sources, -1) != nil || inspectorSourcePreviews(nil, 2) != nil {
		t.Fatal("preview bounds should accept empty, zero, negative, and oversized limits")
	}
}

func TestInspectorSourceLabelsUseResolvedValueSourceTargets(t *testing.T) {
	for _, tc := range []struct {
		source introspect.ValueSource
		want   string
	}{
		{introspect.ValueSource{Kind: "bundle-values", Name: "/work/values/api.yaml", MaterializedFile: "/cache/rendered.yaml"}, "/work/values/api.yaml"},
		{introspect.ValueSource{Kind: "packaged-default", Name: "values.yaml", MaterializedFile: "/package-view/api/values.yaml"}, "/package-view/api/values.yaml"},
		{introspect.ValueSource{Kind: "operator-file", Name: "not-resolved.yaml"}, "File unavailable"},
	} {
		source := DetailItem{Kind: DetailItemSource, File: traceSourceFile(tc.source), Label: friendlySourceLabel("api", tc.source, 0)}
		got := inspectorSourceLabel(source)
		if !strings.HasPrefix(got, tc.want+" · line unavailable · ") {
			t.Fatalf("display changed or invented a resolved source target: %q, want %q", got, tc.want)
		}
		if tc.source.Name == "/work/values/api.yaml" && strings.Contains(got, "/cache/rendered.yaml") {
			t.Fatal("rendered cache replaced the author's recorded source file")
		}
	}
}

func TestLabelInspectorSourcesUsesUniqueFilenamesAndPreservesTargets(t *testing.T) {
	sources := []DetailItem{
		{Kind: DetailItemSource, File: "/home/reader/project/templates/deployment.yaml", Label: "Template",
			Location: introspect.ValueUsageLocation{File: "/home/reader/project/templates/deployment.yaml", Line: 12, Column: 4}},
		{Kind: DetailItemSource, File: "/home/reader/project/values.yaml", Label: "Chart value declaration",
			Location: introspect.ValueUsageLocation{File: "/home/reader/project/values.yaml", Line: 3}},
		{Kind: DetailItemSource, File: "/home/reader/project/templates/deployment.yaml", Label: "Template",
			Location: introspect.ValueUsageLocation{File: "/home/reader/project/templates/deployment.yaml", Line: 27}},
	}
	before := append([]DetailItem(nil), sources...)
	model := Model{}
	labeled := model.labelInspectorSources(sources)
	want := []string{"deployment.yaml:12 · Template", "values.yaml:3 · Chart value declaration", "deployment.yaml:27 · Template"}
	for i, source := range labeled {
		if source.Label != want[i] {
			t.Fatalf("source %d: got %q, want %q", i, source.Label, want[i])
		}
		copy := source
		copy.Label, copy.sourceRole, copy.sourceDisplayPath = before[i].Label, before[i].sourceRole, before[i].sourceDisplayPath
		if !reflect.DeepEqual(copy, before[i]) {
			t.Fatalf("labeling changed recorded source/editor identity: %+v", copy)
		}
	}
	if !reflect.DeepEqual(sources, before) {
		t.Fatal("labeling mutated its input slice")
	}
	if !reflect.DeepEqual(model.labelInspectorSources(labeled), labeled) {
		t.Fatal("batch labeling is not idempotent")
	}
	if got := inspectorSourcePreviews(labeled, 2); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("previews replaced shortened labels: %q", got)
	}
}

func TestLabelInspectorSourcesDisambiguatesBeforePreviewLimit(t *testing.T) {
	sources := []DetailItem{
		{File: "/work/project/api/templates/config.yaml", Label: "Template"},
		{File: "/work/project/worker/templates/config.yaml", Label: "Template"},
	}
	labeled := (Model{}).labelInspectorSources(sources)
	for i, want := range []string{"api/templates/config.yaml", "worker/templates/config.yaml"} {
		if labeled[i].sourceDisplayPath != want || labeled[i].Label != want+" · Template" {
			t.Fatalf("source %d did not use its shortest distinct suffix: %+v", i, labeled[i])
		}
	}
	if got := inspectorSourcePreviews(labeled, 1); len(got) != 1 || !strings.HasPrefix(got[0], "api/templates/config.yaml") {
		t.Fatalf("preview truncation lost source-set ambiguity: %q", got)
	}
}

func TestLabelInspectorSourcesUsesChartOwnershipInsteadOfCacheRoots(t *testing.T) {
	const rootA = "/tmp/neutrino/package-views/0123456789abcdef/0/charts/shared"
	const rootB = "/tmp/neutrino/package-views/fedcba9876543210/2/charts/shared"
	model := Model{components: map[string]*introspect.IntrospectResult{
		"alpha/component/shared": {Source: rootA},
		"beta/component/shared":  {Source: rootB},
	}}
	sources := []DetailItem{
		{File: rootA + "/values.yaml", Label: "Chart value declaration"},
		{File: rootB + "/values.yaml", Label: "Chart value declaration"},
	}
	labeled := model.labelInspectorSources(sources)
	for i, want := range []string{"alpha/component/shared/values.yaml", "beta/component/shared/values.yaml"} {
		if labeled[i].sourceDisplayPath != want {
			t.Fatalf("source %d lost chart/package ownership: %q", i, labeled[i].Label)
		}
	}
	// Relative paths with different recorded owners are not the same file.
	sources[0].File, sources[0].sourceComponent = "values.yaml", "alpha/component/shared"
	sources[1].File, sources[1].sourceComponent = "values.yaml", "beta/component/shared"
	relative := model.labelInspectorSources(sources)
	if relative[0].sourceDisplayPath != labeled[0].sourceDisplayPath || relative[1].sourceDisplayPath != labeled[1].sourceDisplayPath {
		t.Fatalf("relative chart identities collapsed: %+v", relative)
	}
}

func TestLabelInspectorSourcesUsesDeclaredPackageFilenames(t *testing.T) {
	model := Model{report: introspect.InspectionReport{PackageValues: []introspect.PackageValues{
		{PackageName: "alpha", Sources: []introspect.ValueSource{{Name: "inputs/values.yaml", MaterializedFile: "/tmp/cache/abcdef12/a/rendered.yaml"}}},
		{PackageName: "beta", Sources: []introspect.ValueSource{{Name: "inputs/values.yaml", MaterializedFile: "/tmp/cache/fedcba98/b/rendered.yaml"}}},
	}}}
	sources := []DetailItem{
		{File: "/tmp/cache/abcdef12/a/rendered.yaml", Label: "alpha / packaged defaults 1"},
		{File: "/tmp/cache/fedcba98/b/rendered.yaml", Label: "beta / packaged defaults 1"},
	}
	labeled := model.labelInspectorSources(sources)
	for i, want := range []string{"alpha/inputs/values.yaml", "beta/inputs/values.yaml"} {
		if labeled[i].sourceDisplayPath != want || labeled[i].sourceRole != sources[i].Label || labeled[i].File != sources[i].File {
			t.Fatalf("declared filename or exact target lost: %+v", labeled[i])
		}
	}
}

func TestLabelInspectorSourcesDoesNotExposeCacheOnlyDifferences(t *testing.T) {
	for _, paths := range [][2]string{
		{"/tmp/neutrino/package-views/0123456789abcdef/0/values.yaml", "/tmp/neutrino/package-views/fedcba9876543210/1/values.yaml"},
		{"/home/reader/.cache/neutrino/sha256-abcdef/templates/config.yaml", "/home/reader/.cache/neutrino/sha256-fedcba/templates/config.yaml"},
		{"/tmp/neutrino/package-blobs/0123456789abcdef", "/tmp/neutrino/package-blobs/fedcba9876543210"},
		{`C:\Users\first\project\values.yaml`, `C:\Users\second\project\values.yaml`},
	} {
		sources := []DetailItem{{File: paths[0], Label: "Values source"}, {File: paths[1], Label: "Values source"}}
		labeled := (Model{}).labelInspectorSources(sources)
		if labeled[0].Label == labeled[1].Label || !strings.Contains(labeled[0].Label, "[source 1]") || !strings.Contains(labeled[1].Label, "[source 2]") {
			t.Fatalf("indistinguishable logical files need stable source identities: %+v", labeled)
		}
		for _, source := range labeled {
			for _, forbidden := range []string{"/tmp", "/home", "reader", "Users", "first", "second", "0123456789abcdef", "fedcba9876543210", "sha256", ".cache", "package-views"} {
				if strings.Contains(source.Label, forbidden) {
					t.Fatalf("source label exposed transport identity %q: %q", forbidden, source.Label)
				}
			}
		}
	}
}

func TestLabelInspectorSourcesKeepsCoordinateKnowledgeAndWindowsNames(t *testing.T) {
	sources := []DetailItem{
		{File: `C:\Users\reader\project\api\values.yaml`, Label: "Values source", Location: introspect.ValueUsageLocation{Line: 5}},
		{File: `C:\Users\reader\project\worker\values.yaml`, Label: "Values source", Location: introspect.ValueUsageLocation{Line: 9}},
		{File: "operator.yaml", Label: "Operator input", Location: introspect.ValueUsageLocation{File: "template.yaml", Line: 42}},
		{Label: "Template", Location: introspect.ValueUsageLocation{Line: 7}},
		{Label: "Values source"},
	}
	labeled := (Model{}).labelInspectorSources(sources)
	want := []string{"api/values.yaml:5 · Values source", "worker/values.yaml:9 · Values source", "operator.yaml · Operator input", "File unavailable · line 7 · Template", "File unavailable · Values source"}
	for i := range labeled {
		if labeled[i].Label != want[i] {
			t.Fatalf("source %d: got %q, want %q", i, labeled[i].Label, want[i])
		}
	}
	if labeled[3].sourceDisplayPath != "" || labeled[4].sourceDisplayPath != "" {
		t.Fatal("unavailable files acquired an invented display path")
	}
	if (Model{}).labelInspectorSources(nil) != nil {
		t.Fatal("nil input should remain nil")
	}
}

func TestLabelInspectorSourcesPreservesDeclaredHexDirectories(t *testing.T) {
	for _, directory := range []string{"", "cache/", "/work/project/"} {
		sources := []DetailItem{
			{File: directory + "deadbeef/values.yaml", Label: "Operator input"},
			{File: directory + "cafebabe/values.yaml", Label: "Operator input"},
		}
		labeled := (Model{}).labelInspectorSources(sources)
		if labeled[0].sourceDisplayPath != "deadbeef/values.yaml" || labeled[1].sourceDisplayPath != "cafebabe/values.yaml" {
			t.Fatalf("meaningful authored directories were treated as cache digests: %+v", labeled)
		}
	}
}
