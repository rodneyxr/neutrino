package introspect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/api/v1beta1"
)

func TestParseZarfDefinitionAlpha(t *testing.T) {
	for _, version := range []string{"", v1alpha1.APIVersion} {
		t.Run("version="+version, func(t *testing.T) {
			definition := `kind: ZarfPackageConfig
metadata:
  name: alpha
values:
  files: [first.yaml, second.yaml]
  schema: values.schema.json
variables:
  - name: DOMAIN
    default: example.test
constants:
  - name: CREATOR
    value: author
components:
  - name: unspecified
    charts:
      - name: app
        version: 1.0.0
        localPath: chart
        valuesFiles: [static.yaml]
        templatedValuesFiles: [runtime.yaml]
  - name: required
    required: true
  - name: optional
    required: false
`
			if version != "" {
				definition = "apiVersion: " + version + "\n" + definition
			}
			pkg, err := parseZarfDefinition([]byte(definition))
			require.NoError(t, err)
			require.Equal(t, version, pkg.APIVersion)
			require.Equal(t, v1alpha1.APIVersion, pkg.GetAPIVersion())
			require.Equal(t, "alpha", pkg.Metadata.Name)
			require.Equal(t, []string{"first.yaml", "second.yaml"}, pkg.Values.Files)
			require.Equal(t, "values.schema.json", pkg.Values.Schema)
			require.Len(t, pkg.Variables, 1)
			require.Equal(t, "example.test", pkg.Variables[0].Default)
			require.Len(t, pkg.Constants, 1)
			require.Equal(t, "author", pkg.Constants[0].Value)
			require.Len(t, pkg.Components, 3)
			require.Equal(t, map[string]bool{"unspecified": false, "required": false, "optional": true}, pkg.optionalComponents)
			require.True(t, pkg.Components[0].Optional, "preserve upstream deployment semantics independently of reader optionality")
			chart := pkg.Components[0].Charts[0]
			require.Equal(t, "1.0.0", chart.LegacyVersion)
			require.Equal(t, "chart", chart.LocalPath())
			require.Equal(t, []api.ValuesFile{{Path: "static.yaml"}, {Path: "runtime.yaml", EnableTemplating: true}}, chart.ValuesFiles)
		})
	}
}

func TestParseZarfDefinitionBetaPreservesValuesFileOrder(t *testing.T) {
	pkg, err := parseZarfDefinition([]byte(`apiVersion: zarf.dev/v1beta1
kind: ZarfPackageConfig
metadata:
  name: beta
values:
  files: [defaults.yaml, site.yaml]
components:
  - name: apps
    charts:
      - name: app
        local:
          path: chart
        valuesFiles:
          - path: before.yaml
          - path: runtime.yaml
            enableTemplating: true
          - path: after.yaml
        values:
          - sourcePath: .app
            targetPath: .
            excludePaths: [.app.secret]
  - name: optional
    optional: true
`))
	require.NoError(t, err)
	require.Equal(t, v1beta1.APIVersion, pkg.APIVersion)
	require.Equal(t, []string{"defaults.yaml", "site.yaml"}, pkg.Values.Files)
	require.Equal(t, map[string]bool{"apps": false, "optional": true}, pkg.optionalComponents)
	require.Len(t, pkg.Components, 2)
	require.Len(t, pkg.Components[0].Charts, 1)
	chart := pkg.Components[0].Charts[0]
	require.Equal(t, "chart", chart.LocalPath())
	require.Empty(t, chart.LegacyVersion)
	require.Equal(t, []api.ValuesFile{
		{Path: "before.yaml"},
		{Path: "runtime.yaml", EnableTemplating: true},
		{Path: "after.yaml"},
	}, chart.ValuesFiles)
	require.Equal(t, []api.ChartValue{{SourcePath: ".app", TargetPath: ".", ExcludePaths: []string{".app.secret"}}}, chart.Values)
}

func TestZarfRemoteChartSourceReferences(t *testing.T) {
	const chartURL = "oci://registry.example.test/charts/app"
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name       string
		apiVersion string
		chart      string
		want       ChartSource
	}{
		{
			name:  "alpha digest remains in URL",
			chart: "        url: " + chartURL + "@" + digest + "\n        version: 1.2.3\n",
			want:  ChartSource{URL: chartURL + "@" + digest, RepoName: "app"},
		},
		{
			name:       "beta digest remains in URL",
			apiVersion: v1beta1.APIVersion,
			chart:      "        oci:\n          url: " + chartURL + "\n          ref:\n            digest: " + digest + "\n",
			want:       ChartSource{URL: chartURL + "@" + digest, RepoName: "app"},
		},
		{
			name:  "alpha version remains a tag",
			chart: "        url: " + chartURL + "\n        version: 1.2.3\n",
			want:  ChartSource{URL: chartURL, Version: "1.2.3", RepoName: "app"},
		},
		{
			name:       "beta tag remains a version",
			apiVersion: v1beta1.APIVersion,
			chart:      "        oci:\n          url: " + chartURL + "\n          ref:\n            tag: 1.2.3\n",
			want:       ChartSource{URL: chartURL, Version: "1.2.3", RepoName: "app"},
		},
		{
			name:       "beta Helm repository retains chart name",
			apiVersion: v1beta1.APIVersion,
			chart:      "        helmRepository:\n          url: https://charts.example.test\n          name: upstream-app\n          version: 1.2.3\n",
			want:       ChartSource{URL: "https://charts.example.test", Version: "1.2.3", RepoName: "upstream-app"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := "kind: ZarfPackageConfig\nmetadata:\n  name: references\ncomponents:\n  - name: apps\n    charts:\n      - name: app\n" + tc.chart
			if tc.apiVersion != "" {
				definition = "apiVersion: " + tc.apiVersion + "\n" + definition
			}
			pkg, err := parseZarfDefinition([]byte(definition))
			require.NoError(t, err)
			require.Len(t, pkg.Components, 1)
			require.Len(t, pkg.Components[0].Charts, 1)
			require.Equal(t, tc.want, remoteChartSource(pkg.Components[0].Charts[0]))
		})
	}
}

func TestParseZarfDefinitionSelectsNewestSupportedDocument(t *testing.T) {
	alpha := "apiVersion: zarf.dev/v1alpha1\nmetadata: {name: alpha}\n"
	beta := "apiVersion: zarf.dev/v1beta1\nmetadata: {name: beta}\n"
	unknown := "apiVersion: zarf.dev/v99\nmetadata: [unsupported, format]\n"
	for _, docs := range [][]string{
		{alpha, beta},
		{beta, alpha},
		{unknown, alpha, beta},
		{beta, unknown, unknown, alpha},
	} {
		t.Run(strings.Join(docs, "---\n"), func(t *testing.T) {
			pkg, err := parseZarfDefinition([]byte(strings.Join(docs, "---\n")))
			require.NoError(t, err)
			require.Equal(t, v1beta1.APIVersion, pkg.APIVersion)
			require.Equal(t, "beta", pkg.Metadata.Name)
		})
	}
	// Blank documents do not hide the supported definition, and inspection does
	// not require the metadata/components that deployment validation would demand.
	pkg, err := parseZarfDefinition([]byte("---\n" + unknown + "---\n" + alpha + "---\n"))
	require.NoError(t, err)
	require.Equal(t, "alpha", pkg.Metadata.Name)
}

func TestParseZarfDefinitionRejectsAmbiguousOrUnsupportedDocuments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
		want     string
	}{
		{"duplicate alpha", "apiVersion: zarf.dev/v1alpha1\n---\napiVersion: zarf.dev/v1alpha1\n", "duplicate apiVersion"},
		{"omitted and explicit alpha", "metadata: {name: legacy}\n---\napiVersion: zarf.dev/v1alpha1\n", "duplicate apiVersion"},
		{"duplicate beta", "apiVersion: zarf.dev/v1beta1\n---\napiVersion: zarf.dev/v1beta1\n", "duplicate apiVersion"},
		{"duplicate lower priority", "apiVersion: zarf.dev/v1beta1\n---\nmetadata: {name: legacy}\n---\napiVersion: zarf.dev/v1alpha1\n", "duplicate apiVersion"},
		{"unsupported", "apiVersion: zarf.dev/v99\n", "no supported apiVersion"},
		{"empty", "# only a comment\n", "no package definition"},
		{"invalid version type", "apiVersion: [zarf.dev/v1beta1]\n", "reading apiVersion"},
		{"invalid selected document", "apiVersion: zarf.dev/v1beta1\nmetadata: [invalid]\n", "decoding zarf.dev/v1beta1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseZarfDefinition([]byte(tc.contents))
			require.ErrorContains(t, err, tc.want)
		})
	}
	_, err := parseZarfDefinition([]byte("metadata: [unterminated\n"))
	require.Error(t, err)
}

func TestInspectZarfBetaValuesFilesPreserveInterleavedOrder(t *testing.T) {
	root := writeZarfDefinitionFixture(t, map[string][]byte{
		"zarf.yaml": []byte(`apiVersion: zarf.dev/v1beta1
kind: ZarfPackageConfig
metadata:
  name: interleaved
components:
  - name: apps
    charts:
      - name: app
        local:
          path: chart
        valuesFiles:
          - path: first.yaml
          - path: middle.yaml
            enableTemplating: true
          - path: last.yaml
`),
		"chart/Chart.yaml":                []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"chart/values.yaml":               []byte("imageTag: chart-default\n"),
		"chart/templates/deployment.yaml": []byte("imageTag: {{ .Values.imageTag }}\n"),
		"first.yaml":                      []byte("imageTag: final1\n"),
		"middle.yaml":                     []byte("imageTag: final2\n"),
		"last.yaml":                       []byte("imageTag: final3\n"),
	})

	report, err := InspectZarfPackage(context.Background(), root, "", "amd64", IntrospectOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"apps/app"}, report.ComponentOrder)
	require.Contains(t, report.ChartResults, "apps/app")
	chart := report.ChartResults["apps/app"]
	values := make(map[string]interface{}, len(chart.Values))
	for _, value := range chart.Values {
		values[value.Path] = value.DefaultValue
	}
	// The template-enabled middle file contains plain YAML. Grouping it after
	// all plain files would incorrectly produce final2 instead of final3.
	require.Equal(t, "final3", values["imageTag"])
}

func TestInspectZarfVersionedPackagedChartLayout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		definition string
		chartPath  string
		valuePaths []string
	}{
		{
			name: "beta",
			definition: `apiVersion: zarf.dev/v1beta1
kind: ZarfPackageConfig
metadata:
  name: packaged-beta
components:
  - name: apps
    charts:
      - name: app
        helmRepository:
          name: app
          url: https://unused.invalid/charts
          version: 9.0.0
        valuesFiles:
          - path: first.yaml
          - path: middle.yaml
            enableTemplating: true
          - path: last.yaml
`,
			chartPath:  "charts/app.tgz",
			valuePaths: []string{"values/app-0", "values/app-1", "values/app-2"},
		},
		{
			name: "alpha",
			definition: `apiVersion: zarf.dev/v1alpha1
kind: ZarfPackageConfig
metadata:
  name: packaged-alpha
components:
  - name: apps
    required: true
    charts:
      - name: app
        version: 1.0.0
        localPath: unavailable-source-chart
        valuesFiles: [first.yaml, middle.yaml]
        templatedValuesFiles: [last.yaml]
`,
			chartPath:  "charts/app-1.0.0.tgz",
			valuePaths: []string{"values/app-1.0.0-0", "values/app-1.0.0-1", "values/app-1.0.0-2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{
				tc.chartPath:     chartArchive(t, "app", []byte("imageTag: chart-default\n")),
				tc.valuePaths[0]: []byte("imageTag: final1\n"),
				tc.valuePaths[1]: []byte("imageTag: final2\n"),
				tc.valuePaths[2]: []byte("imageTag: final3\n"),
			}
			component := tarArchive(t, files)
			definition, err := parseZarfDefinition([]byte(tc.definition))
			require.NoError(t, err)
			require.Len(t, definition.Components, 1)
			entries, err := readComponentEntries("components/apps.tar", component)
			require.NoError(t, err)
			selected, err := packagedChartLayout(definition.Components[0], "components/apps.tar", entries)
			require.NoError(t, err)
			require.Equal(t, []packagedChart{{ChartPath: tc.chartPath, ValuesPaths: tc.valuePaths}}, selected)

			root := writeZarfDefinitionFixture(t, map[string][]byte{
				"zarf.yaml":           []byte(tc.definition),
				"components/apps.tar": component,
			})
			report, err := InspectZarfPackage(context.Background(), root, "", "amd64", IntrospectOptions{})
			require.NoError(t, err)
			require.Equal(t, []string{"apps/app"}, report.ComponentOrder)
			require.Contains(t, report.ChartResults, "apps/app")
			chart := report.ChartResults["apps/app"]
			require.Equal(t, "app", chart.ChartName)
			values := make(map[string]interface{}, len(chart.Values))
			for _, value := range chart.Values {
				values[value.Path] = value.DefaultValue
			}
			require.Equal(t, "final3", values["imageTag"])
			require.NotContains(t, chart.Source, "unavailable-source-chart")
		})
	}
}

func writeZarfDefinitionFixture(t *testing.T, files map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, contents, 0600))
	}
	return root
}
