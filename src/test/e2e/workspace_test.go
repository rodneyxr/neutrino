package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/stretchr/testify/require"
)

// The same authored package is inspected directly and twice in a Next bundle.
// This checks ownership, literal bundle rendering, unresolved later inputs, and
// coexistence with legacy variables without executing its failing action.
func TestWorkspaceFixturePreservesConfigurationBoundaries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := filepath.Abs("../workspace")
	require.NoError(t, err)
	direct, err := introspect.InspectZarfPackage(context.Background(), filepath.Join(root, "package"), "", "amd64", introspect.IntrospectOptions{})
	require.NoError(t, err)
	require.Len(t, direct.PackageValues, 1)
	require.EqualValues(t, 4, direct.PackageValues[0].Values["scale"])
	require.Equal(t, false, direct.PackageValues[0].Values["app"].(map[string]interface{})["enabled"])
	require.Len(t, direct.Variables, 1)
	require.Equal(t, "LEGACY_REGION", direct.Variables[0].Name)
	var producer bool
	for _, usage := range direct.PackageValues[0].Usages {
		if usage.Path == ".runtime.endpoint" && usage.Runtime {
			producer = true
		}
	}
	require.True(t, producer, "runtime action output must remain unevaluated")

	bundle, err := introspect.InspectUDSBundle(context.Background(), filepath.Join(root, "next"), "", "amd64", introspect.IntrospectOptions{BundleConfig: filepath.Join(root, "next/config.uds.hcl")})
	require.NoError(t, err)
	require.Equal(t, "next", bundle.Bundle.Format)
	require.Len(t, bundle.PackageValues, 2)
	require.Equal(t, "api", bundle.PackageValues[0].PackageName)
	require.Equal(t, "worker", bundle.PackageValues[1].PackageName)
	require.EqualValues(t, 6, bundle.PackageValues[0].Values["scale"])
	require.EqualValues(t, 4, bundle.PackageValues[1].Values["scale"], "depends_on must not transfer configuration")
	require.Equal(t, []string{"api"}, bundle.Bundle.Packages[1].DependsOn)
	require.Empty(t, bundle.UDSOverrideValues, "Next does not manufacture legacy overrides")
	require.Len(t, bundle.Variables, 2)
	var pending bool
	for _, usage := range bundle.PackageValues[0].Usages {
		if usage.Runtime {
			for _, location := range usage.Locations {
				if filepath.Base(location.File) == "pending.yaml" {
					pending = true
				}
			}
		}
	}
	require.True(t, pending, "missing bundle input must remain visible")
}
