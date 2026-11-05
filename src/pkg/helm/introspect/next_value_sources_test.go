package introspect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNextValueTemplateClassifications(t *testing.T) {
	vars := map[string]interface{}{"site": map[string]interface{}{"host": "example.test"}, "count": 3}
	for _, tt := range []struct {
		name, text string
		status     ValueStatus
		path       string
		computed   bool
	}{
		{"literal", "host: {{ .vars.site.host }}", ValueResolved, ".site.host", false},
		{"index", `host: {{ index .vars "site" "host" }}`, ValueResolved, ".site.host", false},
		{"missing", "host: {{ .vars.absent }}", ValueMissing, ".absent", false},
		{"computed", `host: {{ printf "%s" .vars.site.host }}`, ValueDynamic, ".site.host", true},
		{"control", `{{ if .vars.count }}host: example.test{{ end }}`, ValueDynamic, ".count", true},
		{"runtime", `host: {{ .State.host }}`, ValueDynamic, "", false},
		{"unknown function", `host: {{ runtimeLookup "secret" }}`, ValueDynamic, "", false},
		{"parse error", `host: {{ .vars.site.host`, ValueParseError, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rendered, source := inspectNextValuesTemplate([]byte(tt.text), "bundle.yaml", vars)
			require.Equal(t, tt.status, source.Status)
			if tt.status == ValueResolved {
				require.Equal(t, "host: example.test", string(rendered))
			} else {
				require.Nil(t, rendered, "unresolved templates must not be executed")
				require.NotEmpty(t, source.Diagnostics)
			}
			if tt.path != "" {
				require.Len(t, source.References, 1)
				require.Equal(t, tt.path, source.References[0].Path)
				require.Equal(t, tt.computed, source.References[0].Computed)
				require.Equal(t, "bundle-vars", source.References[0].Scope)
			}
		})
	}
}

func TestIsRemoteAcceptsShortLocalSources(t *testing.T) {
	for _, source := range []string{"", "pkg", "local/", "package", "relative"} {
		require.False(t, IsRemote(source), source)
	}
	for _, source := range []string{"oci://example", "http://example", "https://example"} {
		require.True(t, IsRemote(source), source)
	}
}

func TestNextSourcesPreserveOrderAndUnresolvedAlignment(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(text), 0600))
		return path
	}
	write(nextBundleFile, `uds { bundle_api_version = "uds.dev/v1alpha1" }
metadata { name = "sources" }
package "app" {
 source = "package"
 values_files = ["missing.yaml", "resolved.yaml"]
}
`)
	write(nextDefaultsFile, `variables = { app = { host = "default.test", count = 1 } }`)
	config := write("config.uds.hcl", `variables = { app = { count = 3 } }`)
	write("missing.yaml", "app:\n  host: {{ .vars.absent }}\n")
	resolved := write("resolved.yaml", "app:\n  count: {{ .vars.app.count }}\n")
	operator := write("operator.yaml", "app:\n  count: 4\n")
	write("package/zarf.yaml", "kind: ZarfPackageConfig\nmetadata:\n  name: example\nvalues:\n  files: [defaults.yaml]\nvariables:\n  - name: LEGACY\n    default: retained\n")
	write("package/defaults.yaml", "app:\n  count: 1\n  host: default.test\n")
	got, err := InspectNextUDSBundle(context.Background(), root, "", "amd64", IntrospectOptions{BundleConfig: config, ValuesFiles: []string{operator}})
	require.NoError(t, err)
	require.Len(t, got.Bundle.VariableSources, 2)
	require.EqualValues(t, 1, got.Bundle.VariableSources[0].Values["app"].(map[string]interface{})["count"])
	require.EqualValues(t, 3, got.Bundle.VariableSources[1].Values["app"].(map[string]interface{})["count"])
	require.Len(t, got.Variables, 1)
	require.Equal(t, "app.LEGACY", got.Variables[0].Name)
	require.Len(t, got.PackageValues, 1)
	item := got.PackageValues[0]
	require.Len(t, item.Sources, 4)
	require.Equal(t, []string{"packaged-default", "bundle-values", "bundle-values", "operator-file"}, []string{item.Sources[0].Kind, item.Sources[1].Kind, item.Sources[2].Kind, item.Sources[3].Kind})
	require.Equal(t, ValueMissing, item.Sources[1].Status)
	require.Equal(t, ValueResolved, item.Sources[2].Status)
	require.Equal(t, resolved, item.Sources[2].Name)
	require.NotEqual(t, resolved, item.Sources[2].MaterializedFile)
	require.Equal(t, item.Sources[2].MaterializedFile, item.FileSources[2])
	require.Len(t, item.FileSources, len(item.Files))
	require.EqualValues(t, 1, item.Sources[0].Values["app"].(map[string]interface{})["count"])
	require.EqualValues(t, 3, item.Sources[2].Values["app"].(map[string]interface{})["count"])
	require.EqualValues(t, 4, item.Values["app"].(map[string]interface{})["count"])
	require.Contains(t, item.Scope, "unresolved")
}

func TestValueSourceSerializationDoesNotDuplicateValues(t *testing.T) {
	data, err := json.Marshal(ValueSource{ID: "operator:1", Kind: "operator-file", Name: "values.yaml", Status: ValueResolved,
		Values: map[string]interface{}{"password": "do-not-serialize"}, MaterializedFile: "/private/cache/location"})
	require.NoError(t, err)
	require.NotContains(t, string(data), "do-not-serialize")
	require.NotContains(t, string(data), "/private/cache/location")
}

func TestPackageTemplateRuntimeContextAndDiagnostics(t *testing.T) {
	usages := inspectTemplateText(`token: {{ .State.token }}`, "action", "app", "")
	require.Len(t, usages, 1)
	require.Equal(t, ".State.token", usages[0].Path)
	require.Equal(t, ValueDynamic, usages[0].Status)
	for _, input := range []struct{ text, path string }{
		{`token: {{ index .State "token" }}`, ".State.token"},
		{`token: {{ uuidv4 }}`, "."},
		{"static text in a file declared enableTemplating", "."},
	} {
		found := inspectTemplateText(input.text, "runtime.yaml", "app", "chart")
		require.Len(t, found, 1, input.text)
		require.Equal(t, input.path, found[0].Path)
		require.Equal(t, ValueDynamic, found[0].Status)
		require.True(t, found[0].Runtime)
	}
	item := PackageValues{Values: map[string]interface{}{"present": nil}, Usages: []PackageValueUsage{
		{Path: ".present", TargetPath: ".target"},
		{Path: ".missing", TargetPath: ".other"},
		{Path: ".written", Runtime: true, Locations: []ValueUsageLocation{{Kind: "setValues"}}},
		{Path: ".", Runtime: true, Locations: []ValueUsageLocation{{Kind: "unavailableTemplate"}}},
	}}
	finalizePackageValues(&item)
	require.Equal(t, ValueResolved, item.Usages[0].Status, "explicit null is present")
	require.Equal(t, ValueMissing, item.Usages[1].Status)
	require.Equal(t, ValueDynamic, item.Usages[2].Status)
	require.Equal(t, ValueUnavailable, item.Usages[3].Status)
}
