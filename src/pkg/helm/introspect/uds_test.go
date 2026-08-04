package introspect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBundlePath returns the absolute path to a test bundle's uds-bundle.yaml.
func testBundlePath(t *testing.T, bundleDir string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller info")
	// Navigate from src/pkg/helm/introspect/ -> src/test/bundles/<bundleDir>/
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(repoRoot, "src", "test", "bundles", bundleDir, "uds-bundle.yaml")
}

func TestParseUDSBundleOverrides(t *testing.T) {
	bundlePath := testBundlePath(t, "02-uds-variables")

	bundleBytes, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	var bundle UDSBundle
	err = yaml.Unmarshal(bundleBytes, &bundle)
	require.NoError(t, err)

	assert.Equal(t, "UDSBundle", bundle.Kind)
	assert.Equal(t, "var-test", bundle.Metadata.Name)
	assert.Equal(t, "1.2.3", bundle.Metadata.Version)
	assert.Len(t, bundle.Packages, 2)

	t.Run("zarf-init-s3-backend overrides", func(t *testing.T) {
		pkg := bundle.Packages[0]
		assert.Equal(t, "zarf-init-s3-backend", pkg.Name)
		require.Contains(t, pkg.Overrides, "zarf-agent")
		require.Contains(t, pkg.Overrides["zarf-agent"], "zarf-agent")

		chartOverride := pkg.Overrides["zarf-agent"]["zarf-agent"]
		assert.Empty(t, chartOverride.Values, "zarf-agent should have no hard-coded values")
		require.Len(t, chartOverride.Variables, 1)

		v := chartOverride.Variables[0]
		assert.Equal(t, "ZARF_AGENT_AFFINITY_CUSTOM", v.Name)
		assert.Equal(t, "affinity", v.Path)
		// Default is an empty map {}
		assert.NotNil(t, v.Default)
	})

	t.Run("core-base overrides", func(t *testing.T) {
		pkg := bundle.Packages[1]
		assert.Equal(t, "core-base", pkg.Name)

		// Check istio-controlplane/istiod
		require.Contains(t, pkg.Overrides, "istio-controlplane")
		require.Contains(t, pkg.Overrides["istio-controlplane"], "istiod")
		istiod := pkg.Overrides["istio-controlplane"]["istiod"]
		assert.Empty(t, istiod.Values)
		require.Len(t, istiod.Variables, 2)
		assert.Equal(t, "ISTIO_REQUESTS_MEMORY", istiod.Variables[0].Name)
		assert.Equal(t, "global.proxy.resources.requests.memory", istiod.Variables[0].Path)
		assert.Equal(t, "56Mi", istiod.Variables[0].Default)

		// Check istio-admin-gateway/uds-istio-config has both values and variables
		require.Contains(t, pkg.Overrides, "istio-admin-gateway")
		require.Contains(t, pkg.Overrides["istio-admin-gateway"], "uds-istio-config")
		adminIstioConfig := pkg.Overrides["istio-admin-gateway"]["uds-istio-config"]

		require.Len(t, adminIstioConfig.Values, 1)
		assert.Equal(t, "tls.supportTLSV1_2", adminIstioConfig.Values[0].Path)
		assert.Equal(t, true, adminIstioConfig.Values[0].Value)

		require.Len(t, adminIstioConfig.Variables, 2)
		assert.Equal(t, "ADMIN_TLS_CERT", adminIstioConfig.Variables[0].Name)
		assert.Equal(t, "tls.cert", adminIstioConfig.Variables[0].Path)
		assert.Contains(t, adminIstioConfig.Variables[0].Description, "TLS cert for the admin gateway")

		// Check pepr-uds-core/module has two variables with same name but different paths
		require.Contains(t, pkg.Overrides, "pepr-uds-core")
		require.Contains(t, pkg.Overrides["pepr-uds-core"], "module")
		pepr := pkg.Overrides["pepr-uds-core"]["module"]
		require.Len(t, pepr.Variables, 2)
		assert.Equal(t, "UDS_CORE_NODE_SELECTOR", pepr.Variables[0].Name)
		assert.Equal(t, "watcher.nodeSelector", pepr.Variables[0].Path)
		assert.Equal(t, "UDS_CORE_NODE_SELECTOR", pepr.Variables[1].Name)
		assert.Equal(t, "admission.nodeSelector", pepr.Variables[1].Path)
	})
}

func TestLookupOriginalDefault(t *testing.T) {
	result := &IntrospectResult{
		Values: []ValueUsage{
			{Path: "image.repository", DefaultValue: "nginx"},
			{Path: "image.tag", DefaultValue: "latest"},
			{Path: "replicaCount", DefaultValue: 3},
			{Path: "nodeSelector", DefaultValue: map[string]interface{}{}},
		},
	}

	t.Run("found", func(t *testing.T) {
		assert.Equal(t, "nginx", lookupOriginalDefault(result, "image.repository"))
		assert.Equal(t, 3, lookupOriginalDefault(result, "replicaCount"))
	})

	t.Run("not found", func(t *testing.T) {
		assert.Nil(t, lookupOriginalDefault(result, "nonexistent.path"))
	})

	t.Run("empty map default", func(t *testing.T) {
		val := lookupOriginalDefault(result, "nodeSelector")
		assert.NotNil(t, val)
	})
}

func TestBuildOverridesFromBundle(t *testing.T) {
	// Simulate what InspectUDSBundle does: parse overrides and attach to results
	bundlePath := testBundlePath(t, "02-uds-variables")

	bundleBytes, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	var bundle UDSBundle
	err = yaml.Unmarshal(bundleBytes, &bundle)
	require.NoError(t, err)

	// Create mock IntrospectResults with some chart values for cross-referencing
	mockResults := map[string]*IntrospectResult{
		"core-base/istio-admin-gateway/uds-istio-config": {
			ChartName: "uds-istio-config",
			Values: []ValueUsage{
				{Path: "tls.supportTLSV1_2", DefaultValue: false},
				{Path: "tls.cert", DefaultValue: "original-cert"},
				{Path: "tls.key", DefaultValue: "original-key"},
			},
		},
		"core-base/istio-controlplane/istiod": {
			ChartName: "istiod",
			Values: []ValueUsage{
				{Path: "nodeSelector", DefaultValue: map[string]interface{}{}},
			},
		},
		"core-base/istio-admin-gateway/gateway": {
			ChartName: "gateway",
			Values: []ValueUsage{
				{Path: "service.annotations", DefaultValue: map[string]interface{}{}},
				{Path: "nodeSelector", DefaultValue: map[string]interface{}{}},
			},
		},
	}

	var allOverrideValues []UDSOverrideValueWithContext
	var allOverrideVariables []UDSOverrideVariableWithContext

	for _, pkg := range bundle.Packages {
		pkgName := pkg.Name
		for componentName, charts := range pkg.Overrides {
			for chartName, chartOverride := range charts {
				resultKey := pkgName + "/" + componentName + "/" + chartName
				result := mockResults[resultKey]

				overrideCtx := UDSOverrideContext{
					PackageName:   pkgName,
					ComponentName: componentName,
					ChartName:     chartName,
				}

				for _, bv := range chartOverride.Values {
					ov := UDSOverrideValue{
						Path:  bv.Path,
						Value: bv.Value,
					}
					if result != nil {
						ov.OriginalDefault = lookupOriginalDefault(result, bv.Path)
					}
					if result != nil {
						result.UDSOverrideValues = append(result.UDSOverrideValues, ov)
					}
					allOverrideValues = append(allOverrideValues, UDSOverrideValueWithContext{
						UDSOverrideContext: overrideCtx,
						UDSOverrideValue:   ov,
					})
				}

				for _, bvar := range chartOverride.Variables {
					ov := UDSOverrideVariable{
						Name:        bvar.Name,
						Description: bvar.Description,
						Path:        bvar.Path,
						Default:     bvar.Default,
					}
					if result != nil {
						ov.OriginalDefault = lookupOriginalDefault(result, bvar.Path)
					}
					if result != nil {
						result.UDSOverrideVariables = append(result.UDSOverrideVariables, ov)
					}
					allOverrideVariables = append(allOverrideVariables, UDSOverrideVariableWithContext{
						UDSOverrideContext:  overrideCtx,
						UDSOverrideVariable: ov,
					})
				}
			}
		}
	}

	t.Run("aggregated override values", func(t *testing.T) {
		assert.Len(t, allOverrideValues, 2, "should have 2 override values (admin + tenant TLS)")
		for _, ov := range allOverrideValues {
			assert.Equal(t, "tls.supportTLSV1_2", ov.Path)
			assert.Equal(t, true, ov.Value)
			assert.Equal(t, "core-base", ov.PackageName)
		}
	})

	t.Run("aggregated override variables", func(t *testing.T) {
		assert.Len(t, allOverrideVariables, 15, "should have 15 override variables total")

		// Find ISTIO_REQUESTS_MEMORY
		found := false
		for _, ov := range allOverrideVariables {
			if ov.Name == "ISTIO_REQUESTS_MEMORY" {
				found = true
				assert.Equal(t, "global.proxy.resources.requests.memory", ov.Path)
				assert.Equal(t, "56Mi", ov.Default)
				assert.Equal(t, "core-base", ov.PackageName)
				assert.Equal(t, "istio-controlplane", ov.ComponentName)
				assert.Equal(t, "istiod", ov.ChartName)
				break
			}
		}
		assert.True(t, found, "Expected to find ISTIO_REQUESTS_MEMORY in override variables")
	})

	t.Run("per-component override values with cross-referencing", func(t *testing.T) {
		adminConfig := mockResults["core-base/istio-admin-gateway/uds-istio-config"]
		require.Len(t, adminConfig.UDSOverrideValues, 1)
		assert.Equal(t, "tls.supportTLSV1_2", adminConfig.UDSOverrideValues[0].Path)
		assert.Equal(t, true, adminConfig.UDSOverrideValues[0].Value)
		// Cross-referenced: original default was false
		assert.Equal(t, false, adminConfig.UDSOverrideValues[0].OriginalDefault)
	})

	t.Run("per-component override variables with cross-referencing", func(t *testing.T) {
		adminConfig := mockResults["core-base/istio-admin-gateway/uds-istio-config"]
		require.Len(t, adminConfig.UDSOverrideVariables, 2)

		cert := adminConfig.UDSOverrideVariables[0]
		assert.Equal(t, "ADMIN_TLS_CERT", cert.Name)
		assert.Equal(t, "tls.cert", cert.Path)
		assert.Contains(t, cert.Description, "TLS cert for the admin gateway")
		// Cross-referenced: original default was "original-cert"
		assert.Equal(t, "original-cert", cert.OriginalDefault)

		key := adminConfig.UDSOverrideVariables[1]
		assert.Equal(t, "ADMIN_TLS_KEY", key.Name)
		assert.Equal(t, "original-key", key.OriginalDefault)
	})

	t.Run("per-component override variables cross-ref for gateway", func(t *testing.T) {
		gateway := mockResults["core-base/istio-admin-gateway/gateway"]
		require.Len(t, gateway.UDSOverrideVariables, 2)

		annotations := gateway.UDSOverrideVariables[0]
		assert.Equal(t, "ISTIOD_ADMIN_GW_SVC_ANNOTATIONS", annotations.Name)
		assert.Equal(t, "service.annotations", annotations.Path)
		// Cross-referenced: original default was empty map
		assert.NotNil(t, annotations.OriginalDefault)

		nodeSelector := gateway.UDSOverrideVariables[1]
		assert.Equal(t, "UDS_CORE_NODE_SELECTOR", nodeSelector.Name)
		assert.Equal(t, "nodeSelector", nodeSelector.Path)
		assert.NotNil(t, nodeSelector.OriginalDefault)
	})

	t.Run("override variable without matching result still aggregated", func(t *testing.T) {
		// ZARF_AGENT_AFFINITY_CUSTOM has no mock result, should still be in aggregated list
		found := false
		for _, ov := range allOverrideVariables {
			if ov.Name == "ZARF_AGENT_AFFINITY_CUSTOM" {
				found = true
				assert.Equal(t, "affinity", ov.Path)
				assert.Equal(t, "zarf-init-s3-backend", ov.PackageName)
				assert.Nil(t, ov.OriginalDefault, "should have nil OriginalDefault when no result exists")
				break
			}
		}
		assert.True(t, found, "Expected ZARF_AGENT_AFFINITY_CUSTOM in aggregated variables")
	})
}

func TestParseUDSBundleNoOverrides(t *testing.T) {
	// Verify bundles without overrides parse fine
	bundlePath := testBundlePath(t, "00-podinfo")
	bundleBytes, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	var bundle UDSBundle
	err = yaml.Unmarshal(bundleBytes, &bundle)
	require.NoError(t, err)

	assert.Equal(t, "UDSBundle", bundle.Kind)
	require.Len(t, bundle.Packages, 1)
	assert.Nil(t, bundle.Packages[0].Overrides)
}
