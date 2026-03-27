package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodneyxr/neutrino/pkg/helm/introspect"
	"github.com/stretchr/testify/require"
)

func TestNeutrinoE2E(t *testing.T) {
	ctx := context.Background()

	t.Run("Inspect Remote Podinfo Helm Chart directly", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		// Uses upstream podinfo chart
		// src := introspect.ChartSource{
		// 	URL:     "oci://ghcr.io/stefanprodan/charts/podinfo",
		// 	Version: "6.11.1",
		// }
		src := introspect.ChartSource{
			LocalPath: "../../src/test/packages/podinfo-6.11.1.tgz",
		}
		res, err := introspect.IntrospectHelmValues(ctx, src, opts)
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotEmpty(t, res.Values)

		// Assert that standard dot-notation items are parsed
		foundReplicas := false
		for _, v := range res.Values {
			if v.Path == "replicaCount" {
				foundReplicas = true
				break
			}
		}
		require.True(t, foundReplicas, "Expected to find replicaCount in podinfo chart")
	})

	t.Run("Inspect Zarf Package Baseline (00-podinfo)", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		wd, _ := os.Getwd()
		packagePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "src", "test", "packages", "00-podinfo")

		report, err := introspect.InspectZarfPackage(ctx, packagePath, "", "", opts)
		require.NoError(t, err)
		require.NotNil(t, report)

		require.NotEmpty(t, report.Variables)
		require.Equal(t, "TEST_VAR", report.Variables[0].Name)

		require.NotEmpty(t, report.ComponentOrder)

		comp := report.ChartResults[report.ComponentOrder[0]]
		require.NotEmpty(t, comp.Values)
	})

	t.Run("Inspect UDS Bundle Baseline (00-podinfo)", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		wd, _ := os.Getwd()
		bundlePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "src", "test", "bundles", "00-podinfo")

		report, err := introspect.InspectUDSBundle(ctx, bundlePath, "", "", opts)
		require.NoError(t, err)
		require.NotNil(t, report)

		// Notice that UDS prepends component order keys with package names
		require.NotEmpty(t, report.ComponentOrder)
		key := report.ComponentOrder[0]
		require.Equal(t, "podinfo/podinfo/podinfo", key, "Keys must preserve component bounding")

		require.NotEmpty(t, report.Variables)
	})

	t.Run("Inspect Edge Cases (01-helm-functions)", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		wd, _ := os.Getwd()
		packagePath := filepath.Join(filepath.Dir(filepath.Dir(wd)), "src", "test", "packages", "01-helm-functions")

		report, err := introspect.InspectZarfPackage(ctx, packagePath, "", "", opts)
		require.NoError(t, err)
		require.NotNil(t, report)

		require.NotEmpty(t, report.ComponentOrder)
		comp := report.ChartResults[report.ComponentOrder[0]]
		require.NotEmpty(t, comp.Values)

		// Assert that the `index` function parsed fields accurately
		foundHttpPort := false
		foundExternalIp := false

		for _, v := range comp.Values {
			if v.Path == "ports.http" || v.Path == "Values.ports.http" {
				foundHttpPort = true
				foundTemplateUsage := false
				for _, loc := range v.Locations {
					if strings.HasSuffix(loc.File, "deployment.yaml") {
						foundTemplateUsage = true
						break
					}
				}
				require.True(t, foundTemplateUsage, "Expected to find templates/deployment.yaml usage for ports.http")
			}
			if v.Path == "configs.external-ip" || v.Path == "Values.configs.external-ip" || v.Path == "configs[\"external-ip\"]" {
				foundExternalIp = true
				foundTemplateUsage := false
				for _, loc := range v.Locations {
					if strings.HasSuffix(loc.File, "deployment.yaml") {
						foundTemplateUsage = true
						break
					}
				}
				require.True(t, foundTemplateUsage, "Expected to find templates/deployment.yaml usage for configs.external-ip")
			}
		}

		require.True(t, foundHttpPort, "Expected to successfully parse Helm index function for ports.http")
		require.True(t, foundExternalIp, "Expected to successfully parse Helm index function for configs.external-ip")
	})
}
