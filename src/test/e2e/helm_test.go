package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rodneyxr/neutrino/src/pkg/helm/introspect"
	"github.com/rodneyxr/neutrino/src/test/helpers"
	"github.com/stretchr/testify/require"
)

func TestInspectLocalHelmChart(t *testing.T) {
	ctx := context.Background()

	opts := introspect.IntrospectOptions{
		IncludeSubcharts: true,
		NoCache:          true,
	}
	src := introspect.ChartSource{
		LocalPath: "../../test/packages/podinfo-6.11.1.tgz",
	}
	res, err := introspect.IntrospectHelmValues(ctx, src, opts)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.Values)

	helpers.RequirePathsExist(t, res, []string{"replicaCount"})
}

func TestInspectHelmEdgeCases(t *testing.T) {
	ctx := context.Background()

	opts := introspect.IntrospectOptions{
		IncludeSubcharts: true,
		NoCache:          true,
	}
	wd, _ := os.Getwd()
	packagePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "test", "packages", "01-helm-functions")

	report, err := introspect.InspectZarfPackage(ctx, packagePath, "", "", opts)
	require.NoError(t, err)
	require.NotNil(t, report)

	require.NotEmpty(t, report.ComponentOrder)
	comp := report.ChartResults[report.ComponentOrder[0]]

	// Validate `index` helm function parsing outputs the correct paths
	helpers.RequirePathsExist(t, comp, []string{"ports.http", "configs.external-ip"})
}
