package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rodneyxr/neutrino/src/pkg/helm/introspect"
	"github.com/rodneyxr/neutrino/src/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectLocalZarfPackage(t *testing.T) {
	ctx := context.Background()
	wd, _ := os.Getwd()

	opts := introspect.IntrospectOptions{
		IncludeSubcharts: true,
		NoCache:          true,
	}

	// Map back to the test/packages/00-podinfo directory
	packagePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "test", "packages", "00-podinfo", "zarf-package-podinfo-amd64-0.0.1.tar.zst")

	report, err := introspect.InspectZarfPackage(ctx, packagePath, "", "", opts)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.NotEmpty(t, report.ComponentOrder)

	// Assert on component existence and value paths
	podinfoComp := helpers.RequireComponentExists(t, report, "podinfo/podinfo")
	helpers.RequireChartValues(t, podinfoComp, []string{
		"replicaCount",
		"image.repository",
		"hpa.enabled",
		"ingress.enabled",
	})

	// Assert variables are matched
	helpers.RequireZarfVarsExist(t, report, []string{"TEST_VAR"})
}
