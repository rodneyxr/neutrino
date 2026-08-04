package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/defenseunicorns-dashdays/neutrino/src/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectLocalUDSBundle(t *testing.T) {
	ctx := context.Background()
	wd, _ := os.Getwd()

	opts := introspect.IntrospectOptions{
		IncludeSubcharts: true,
		NoCache:          true,
	}

	// Map back to the test/bundles/00-podinfo directory
	bundlePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "test", "bundles", "00-podinfo", "uds-bundle-00-podinfo-arm64-0.0.1.tar.zst")

	report, err := introspect.InspectUDSBundle(ctx, bundlePath, "", "", opts)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.NotEmpty(t, report.ComponentOrder)

	// In UDS Bundles, component keys are prefixed with the package name.
	// So "podinfo" package -> "podinfo/podinfo/podinfo" (pkg / comp / chart)
	bundleComp := helpers.RequireComponentExists(t, report, "podinfo/podinfo/podinfo")
	helpers.RequireChartValues(t, bundleComp, []string{
		"replicaCount",
		"image.repository",
		"image.tag",
	})

	// Format for uds bundles is <package_name>.<variable_name>
	helpers.RequireZarfVarsExist(t, report, []string{"podinfo.TEST_VAR"})
}
