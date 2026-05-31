package introspect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntrospectHelmValues_SubchartDefaultsAndLocations(t *testing.T) {
	chartDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(chartDir, "templates"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(chartDir, "charts", "child", "templates"), 0755))

	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: parent
version: 0.1.0
dependencies:
  - name: child
    version: 0.1.0
    repository: ""
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(`child:
  other: parent-override
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "charts", "child", "Chart.yaml"), []byte(`apiVersion: v2
name: child
version: 0.1.0
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "charts", "child", "values.yaml"), []byte(`hostRootFsMount:
  enabled: true
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "charts", "child", "templates", "daemonset.yaml"), []byte(`{{- if .Values.hostRootFsMount.enabled }}
kind: DaemonSet
{{- end }}
`), 0644))

	result, err := IntrospectHelmValues(context.Background(), ChartSource{LocalPath: chartDir}, IntrospectOptions{IncludeSubcharts: true})
	require.NoError(t, err)

	var found *ValueUsage
	for i := range result.Values {
		if result.Values[i].Chart == "child" && result.Values[i].Path == "hostRootFsMount.enabled" {
			found = &result.Values[i]
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, true, found.DefaultValue)
	require.NotEmpty(t, found.Locations)
	require.Equal(t, filepath.Join("charts", "child", "templates", "daemonset.yaml"), found.Locations[0].File)

	_, err = os.Stat(filepath.Join(result.Source, found.Locations[0].File))
	require.NoError(t, err)
}
