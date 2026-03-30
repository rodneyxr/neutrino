//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/rodneyxr/neutrino/src/pkg/helm/introspect"
	"github.com/rodneyxr/neutrino/src/test/helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegressionOCI(t *testing.T) {
	ctx := context.Background()

	t.Run("Helm Chart - podinfo", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		src := introspect.ChartSource{
			URL:     "oci://ghcr.io/stefanprodan/charts/podinfo",
			Version: "6.11.1",
		}

		res, err := introspect.IntrospectHelmValues(ctx, src, opts)
		require.NoError(t, err)
		require.NotNil(t, res)

		assert.Equal(t, "podinfo", res.ChartName)
		assert.Equal(t, "6.11.1", res.ChartVersion)

		// Assert deeply nested and key paths are parsed correctly.
		paths := []string{
			"replicaCount",
			"image.repository",
			"ingress.enabled",
			// "ingress.hosts.*.host", // TODO: test wildcard support
			"podDisruptionBudget",
			"probes.liveness.failureThreshold",``
			"redis.enabled",
			"securityContext",
			"tls.certPath",
			"ui.color",
			"ui.logo",
			"ui.message",
		}

		helpers.RequirePathsExist(t, res, paths)
	})

	t.Run("Zarf Package - init", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		path := "oci://ghcr.io/zarf-dev/packages/init:v0.74.0"

		report, err := introspect.InspectZarfPackage(ctx, path, "", "", opts)
		require.NoError(t, err)
		require.NotNil(t, report)

		// Ensure specific Zarf variables are discovered
		expectedVars := []string{
			"REGISTRY_EXISTING_PVC",
			"REGISTRY_PVC_ENABLED",
			"REGISTRY_CPU_REQ",
			"REGISTRY_MEM_REQ",
			"REGISTRY_CPU_LIMIT",
			"REGISTRY_CA_BUNDLE",
			"REGISTRY_EXTRA_ENVS",
			"REGISTRY_AFFINITY_CUSTOM",
			"REGISTRY_TOLERATIONS",
			"REGISTRY_NODE_SELECTOR",
		}

		helpers.RequireZarfVarsExist(t, report, expectedVars)

		// Validate the registry component
		registryComp := helpers.RequireComponentExists(t, report, "zarf-registry/docker-registry", "registry/docker-registry")
		helpers.RequireChartValues(t, registryComp, []string{
			"replicaCount",
			"persistence.enabled",
			"tlsSecretName",
		})
	})

	t.Run("Zarf Package - core-base", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		path := "oci://ghcr.io/defenseunicorns/packages/uds/core-base:1.0.0-upstream"
		arch := "amd64"

		report, err := introspect.InspectZarfPackage(ctx, path, "", arch, opts)
		require.NoError(t, err)
		require.NotNil(t, report)
		assert.NotEmpty(t, report.ComponentOrder)

		// Validate Istio Control Plane
		istioComp := helpers.RequireComponentExists(t, report, "istio-controlplane/base")
		helpers.RequireChartValues(t, istioComp, []string{"global.istioNamespace"})

		// Validate Gateways
		gatewayComponents := []string{
			"istio-admin-gateway/gateway",
			"istio-tenant-gateway/gateway",
			"istio-passthrough-gateway/gateway",
		}

		gatewayPaths := []string{
			"affinity",
			"resources",
			"securityContext",
			"volumeMounts",
			"volumes",
		}

		for _, compKey := range gatewayComponents {
			comp := helpers.RequireComponentExists(t, report, compKey)
			helpers.RequireChartValues(t, comp, gatewayPaths)
		}
	})

	t.Run("UDS Bundle - k3d-core-demo", func(t *testing.T) {
		opts := introspect.IntrospectOptions{
			IncludeSubcharts: true,
			NoCache:          true,
		}
		path := "oci://ghcr.io/defenseunicorns/packages/uds/bundles/k3d-core-demo:1.0.0"

		report, err := introspect.InspectUDSBundle(ctx, path, "", "amd64", opts)
		require.NoError(t, err)
		require.NotNil(t, report)

		assert.NotEmpty(t, report.ComponentOrder)

		// Assert embedded dependencies successfully extract and prefix properly
		foundPrefixCore := false
		foundPrefixInit := false
		for _, compKey := range report.ComponentOrder {
			// e.g. "core/promtail/promtail"
			if len(compKey) > 5 && compKey[:5] == "core/" {
				foundPrefixCore = true
			}
			if len(compKey) > 5 && compKey[:5] == "init/" {
				foundPrefixInit = true
			}
		}
		assert.True(t, foundPrefixCore, "Expected core packages to be extracted and prefixed")
		assert.True(t, foundPrefixInit, "Expected init packages to be extracted and prefixed")
	})
}
