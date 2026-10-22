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
