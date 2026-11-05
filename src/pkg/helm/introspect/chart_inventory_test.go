package introspect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/common"
	v2 "helm.sh/helm/v4/pkg/chart/v2"
)

func writeInventoryChart(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

func inventoryUsage(t *testing.T, result *IntrospectResult, chart, path string) ValueUsage {
	t.Helper()
	var matches []ValueUsage
	for _, usage := range result.Values {
		if usage.Chart == chart && usage.Path == path {
			matches = append(matches, usage)
		}
	}
	require.Len(t, matches, 1, "expected exactly one %s:%s", chart, path)
	return matches[0]
}

func TestChartInventoryIncludesUntouchedDefaultsAndPresence(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
		"values.yaml": `unused:
  enabled: false
  replicas: 0
  text: ""
  nullable: null
  object: {}
  list: []
  dotted.key: hidden
`,
		"templates/config.yaml": "{{ .Values.templateOnly }}\n",
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	for _, path := range []string{"unused", "unused.enabled", "unused.replicas", "unused.text", "unused.nullable", "unused.object", "unused.list"} {
		usage := inventoryUsage(t, result, "inventory", path)
		require.True(t, usage.DefaultPresent, path)
		require.NotEmpty(t, usage.Locations, path)
		require.Equal(t, "values.yaml", usage.Locations[0].File)
		require.Positive(t, usage.Locations[0].Line)
	}
	require.Equal(t, false, inventoryUsage(t, result, "inventory", "unused.enabled").DefaultValue)
	require.Equal(t, "", inventoryUsage(t, result, "inventory", "unused.text").DefaultValue)
	require.Nil(t, inventoryUsage(t, result, "inventory", "unused.nullable").DefaultValue)
	require.False(t, inventoryUsage(t, result, "inventory", "templateOnly").DefaultPresent)
	for _, usage := range result.Values {
		require.NotEqual(t, "unused.dotted.key", usage.Path)
	}

	// A chart with no templates still exposes its complete declared defaults.
	require.NoError(t, os.Remove(filepath.Join(root, "templates", "config.yaml")))
	result, err = IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	require.True(t, inventoryUsage(t, result, "inventory", "unused.enabled").DefaultPresent)
}

func TestChartInventoryPreservesPublicPathsForInternalDefaults(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
		"values.yaml": `_internal_defaults_do_not_set:
  global:
    proxy:
      tracer: none
      resources:
        requests:
          cpu: 100m
  unused:
    enabled: false
`,
		"templates/config.yaml": "{{ .Values.global.proxy.tracer }}\n",
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	resources := inventoryUsage(t, result, "inventory", "global.proxy.resources.requests.cpu")
	require.True(t, resources.DefaultPresent)
	require.Equal(t, "100m", resources.DefaultValue)
	require.Equal(t, []ValueUsageLocation{{File: "values.yaml", Line: 7, Column: 11, Kind: "value"}}, resources.Locations)
	require.Equal(t, false, inventoryUsage(t, result, "inventory", "unused.enabled").DefaultValue)
	for _, usage := range result.Values {
		require.NotContains(t, usage.Path, "_internal_defaults_do_not_set", "inferred public paths must not duplicate private wrapper rows")
	}

	result, err = IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{NoValues: true})
	require.NoError(t, err)
	require.Len(t, result.Values, 1)
	tracer := inventoryUsage(t, result, "inventory", "global.proxy.tracer")
	require.True(t, tracer.DefaultPresent)
	require.Equal(t, "none", tracer.DefaultValue)
}

func TestChartInventorySchemaPropertiesAreAnnotations(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml":            "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
		"values.yaml":           "mode: fast\n",
		"templates/config.yaml": "{{ .Values.mode }}\n",
		"values.schema.json": `{
  "type":"object", "required":["mode", "endpoint"],
  "$defs":{"hidden":{"properties":{"notAnInput":{"type":"string"}}}},
  "properties":{
    "mode":{"type":"string","enum":["fast","safe"],"default":"safe"},
    "endpoint":{"type":"string","minLength":1,"default":"https://example.invalid"},
    "client":{"type":"object","required":["port"],"properties":{"port":{"type":"integer","minimum":1,"maximum":65535}}},
    "nullable":{"default":null},
    "forbidden":false,
    "reference":{"$ref":"#/$defs/hidden"}
  }
}`,
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	require.Len(t, result.ValuesSchemas, 1)
	require.Equal(t, "values.schema.json", result.ValuesSchemas[0].File)
	require.Empty(t, result.ValuesSchemas[0].Diagnostic)
	mode := inventoryUsage(t, result, "inventory", "mode")
	require.Equal(t, "fast", mode.DefaultValue)
	require.True(t, mode.DefaultPresent)
	require.True(t, mode.SchemaRequired)
	require.Equal(t, "safe", mode.Schema.(map[string]interface{})["default"])
	endpoint := inventoryUsage(t, result, "inventory", "endpoint")
	require.Nil(t, endpoint.DefaultValue)
	require.False(t, endpoint.DefaultPresent, "schema default must remain an annotation")
	require.True(t, endpoint.SchemaRequired)
	require.Equal(t, "schema", endpoint.Locations[0].Kind)
	require.Equal(t, "values.schema.json", endpoint.Locations[0].Position())
	port := inventoryUsage(t, result, "inventory", "client.port")
	require.True(t, port.SchemaRequired)
	require.False(t, port.DefaultPresent)
	require.Equal(t, float64(65535), port.Schema.(map[string]interface{})["maximum"])
	require.False(t, inventoryUsage(t, result, "inventory", "client").SchemaRequired)
	require.Equal(t, false, inventoryUsage(t, result, "inventory", "forbidden").Schema)
	require.False(t, inventoryUsage(t, result, "inventory", "nullable").DefaultPresent)
	for _, usage := range result.Values {
		require.NotEqual(t, "reference.notAnInput", usage.Path, "references are retained, not expanded")
		require.NotEqual(t, "notAnInput", usage.Path, "$defs do not declare input paths")
	}

	result, err = IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{NoValues: true})
	require.NoError(t, err)
	require.Len(t, result.Values, 1, "NoValues suppresses additional default/schema rows")
	require.Len(t, result.ValuesSchemas, 1, "NoValues still retains original schemas")
	mode = inventoryUsage(t, result, "inventory", "mode")
	require.True(t, mode.DefaultPresent)
	require.True(t, mode.SchemaRequired)
	require.NotNil(t, mode.Schema)
}

func TestChartInventorySubchartAliasesAndNullOverride(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml": `apiVersion: v2
name: parent
version: 0.1.0
dependencies:
  - name: child
    alias: primary
    version: 0.1.0
    repository: ""
  - name: child
    alias: secondary
    version: 0.1.0
    repository: ""
`,
		"values.yaml":                        "primary: null\nsecondary:\n  enabled: false\n",
		"charts/child/Chart.yaml":            "apiVersion: v2\nname: child\nversion: 0.1.0\n",
		"charts/child/values.yaml":           "enabled: true\nuntouched: child-default\nnullable: null\n",
		"charts/child/templates/config.yaml": "{{ .Values.enabled }}\n",
		"charts/child/values.schema.json":    `{"properties":{"endpoint":{"type":"string","default":"annotation"}}}`,
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{IncludeSubcharts: true})
	require.NoError(t, err)
	primary := inventoryUsage(t, result, "child", "primary.enabled")
	require.False(t, primary.DefaultPresent, "explicit null replaces dependency defaults")
	require.Nil(t, primary.DefaultValue)
	secondary := inventoryUsage(t, result, "child", "secondary.enabled")
	require.True(t, secondary.DefaultPresent)
	require.Equal(t, false, secondary.DefaultValue)
	require.Equal(t, "child-default", inventoryUsage(t, result, "child", "secondary.untouched").DefaultValue)
	require.True(t, inventoryUsage(t, result, "child", "secondary.nullable").DefaultPresent)
	require.False(t, inventoryUsage(t, result, "child", "primary.endpoint").DefaultPresent)
	require.False(t, inventoryUsage(t, result, "child", "secondary.endpoint").DefaultPresent)
	require.Len(t, result.ValuesSchemas, 2)
	require.Equal(t, "primary", result.ValuesSchemas[0].ValuePrefix)
	require.Equal(t, "secondary", result.ValuesSchemas[1].ValuePrefix)
	require.Equal(t, []string{"values.yaml", filepath.Join("charts", "child", "values.yaml")}, result.ValuesFiles)
	for _, location := range secondary.Locations {
		_, err := os.Stat(filepath.Join(result.Source, location.File))
		require.NoError(t, err)
	}

	result, err = IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	require.Empty(t, result.ValuesSchemas)
	for _, usage := range result.Values {
		require.Equal(t, "parent", usage.Chart, "disabled dependency inspection must stay disabled")
	}
}

func TestChartInventoryAliasesSelectCompatiblePackagedVersion(t *testing.T) {
	child := func(version, defaultValue string) *v2.Chart {
		return &v2.Chart{
			Metadata: &v2.Metadata{Name: "child", Version: version},
			Values:   map[string]interface{}{"selected": defaultValue},
			Schema:   []byte(`{"properties":{"selected":{"const":"` + defaultValue + `"}}}`),
			Templates: []*common.File{{
				Name: "templates/config.yaml", Data: []byte("{{ .Values.selected }}"),
			}},
		}
	}
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1-first", true: "v2-first"}[reverse], func(t *testing.T) {
			parent := &v2.Chart{Metadata: &v2.Metadata{
				Name: "parent", Version: "0.1.0",
				Dependencies: []*v2.Dependency{
					{Name: "child", Alias: "primary", Version: "~1.0.0"},
					{Name: "child", Alias: "secondary", Version: "^2.0.0"},
					{Name: "child", Alias: "replica", Version: "~1.0.0"},
				},
			}}
			v1, v2 := child("1.0.2", "version-one"), child("2.3.0", "version-two")
			if reverse {
				parent.AddDependency(v2, v1)
			} else {
				parent.AddDependency(v1, v2)
			}
			// A later compatible version must not claim an already selected alias.
			parent.AddDependency(child("1.0.9", "not-selected"))
			result := &IntrospectResult{ChartName: "parent"}
			opts := IntrospectOptions{IncludeSubcharts: true}
			analyzeChart(parent, result, opts, "", "")
			inventoryChart(context.Background(), parent, result, opts, "", "", parent.Values)
			require.Len(t, result.Values, 3)
			require.Len(t, result.ValuesSchemas, 3, "each alias selects one compatible packaged chart")
			for alias, expected := range map[string]string{"primary": "version-one", "secondary": "version-two", "replica": "version-one"} {
				usage := inventoryUsage(t, result, "child", alias+".selected")
				require.Equal(t, expected, usage.DefaultValue)
				require.True(t, usage.DefaultPresent)
				require.Equal(t, expected, usage.Schema.(map[string]interface{})["const"])
				require.Len(t, usage.Locations, 2, "one template reference and one schema property")
			}
		})
	}
}

func TestChartInventoryFileReplacementDoesNotEraseDeclaredPath(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml":    "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
		"values.yaml":   "service:\n  port: 80\n_internal_defaults_do_not_set:\n  service:\n    port: 90\n",
		"override.yaml": "service: null\n",
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root, ValuesFiles: []string{filepath.Join(root, "override.yaml")}}, IntrospectOptions{})
	require.NoError(t, err)
	require.True(t, inventoryUsage(t, result, "inventory", "service").DefaultPresent)
	require.Nil(t, inventoryUsage(t, result, "inventory", "service").DefaultValue)
	require.False(t, inventoryUsage(t, result, "inventory", "service.port").DefaultPresent, "explicit null must block wrapper fallback")
}

func TestChartInventoryPreservesAllDocumentPathsBeforeAncestorReplacement(t *testing.T) {
	for _, externalOverride := range []bool{false, true} {
		t.Run(map[bool]string{false: "later-document", true: "supplied-file"}[externalOverride], func(t *testing.T) {
			root := t.TempDir()
			values := "first: x\n---\nservice:\n  port: 80\n---\n\n---\nother: \"y\"\n"
			source := ChartSource{LocalPath: root}
			if externalOverride {
				source.ValuesFiles = []string{filepath.Join(root, "override.yaml")}
			} else {
				values += "---\nservice: null\n"
			}
			writeInventoryChart(t, root, map[string]string{
				"Chart.yaml":    "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
				"values.yaml":   values,
				"override.yaml": "service: null\n",
			})
			result, err := IntrospectHelmValues(context.Background(), source, IntrospectOptions{})
			require.NoError(t, err)
			port := inventoryUsage(t, result, "inventory", "service.port")
			require.False(t, port.DefaultPresent)
			require.Nil(t, port.DefaultValue)
			require.Equal(t, "values.yaml", port.Locations[0].File)
			require.Equal(t, 4, port.Locations[0].Line)
			require.True(t, inventoryUsage(t, result, "inventory", "service").DefaultPresent)
			require.Equal(t, "x", inventoryUsage(t, result, "inventory", "first").DefaultValue)
			require.Equal(t, "y", inventoryUsage(t, result, "inventory", "other").DefaultValue)
		})
	}
}

func TestChartInventoryMalformedSchemaIsDiagnostic(t *testing.T) {
	root := t.TempDir()
	writeInventoryChart(t, root, map[string]string{
		"Chart.yaml":         "apiVersion: v2\nname: inventory\nversion: 0.1.0\n",
		"values.yaml":        "enabled: true\n",
		"values.schema.json": "{invalid",
	})
	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: root}, IntrospectOptions{})
	require.NoError(t, err)
	require.True(t, inventoryUsage(t, result, "inventory", "enabled").DefaultPresent)
	require.Len(t, result.ValuesSchemas, 1)
	require.NotEmpty(t, result.ValuesSchemas[0].Diagnostic)
	_, err = json.Marshal(result)
	require.NoError(t, err, "malformed raw JSON must not break report serialization")
}
