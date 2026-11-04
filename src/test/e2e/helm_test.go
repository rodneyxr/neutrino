package e2e

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
	"github.com/defenseunicorns-dashdays/neutrino/src/test/helpers"
	"github.com/stretchr/testify/assert"
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

// TestInspectIstiodChart validates comprehensive value discovery for the istiod chart.
// This test specifically verifies:
//   - Values referenced inside {{define}} blocks are discovered (e.g. global.proxy.tracer)
//   - Sibling values are inferred via parent-path expansion (e.g. global.proxy.resources)
//   - The _internal_defaults_do_not_set fallback resolves values correctly
func TestInspectIstiodChart(t *testing.T) {
	ctx := context.Background()

	opts := introspect.IntrospectOptions{
		IncludeSubcharts: true,
		NoCache:          true,
	}
	src := introspect.ChartSource{
		LocalPath: "../../test/packages/istiod-1.29.1.tgz",
	}
	res, err := introspect.IntrospectHelmValues(ctx, src, opts)
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "istiod", res.ChartName)
	assert.Equal(t, "1.29.1", res.ChartVersion)

	// Collect all discovered paths for comparison
	discoveredPaths := make(map[string]bool)
	for _, v := range res.Values {
		discoveredPaths[v.Path] = true
	}

	// All expected value paths from the istiod chart.
	expectedPaths := []string{
		// Whole-file inventory also exposes unreferenced defaults and parent
		// objects, using the chart's public paths rather than duplicating its
		// private _internal_defaults_do_not_set storage tree.
		"base",
		"cni",
		"cni.enabled",
		"cni.provider",
		"cpu",
		"experimental",
		"global",
		"istiodRemote",
		"memory",
		"sidecarInjectorWebhook",
		"sidecarInjectorWebhookAnnotations",
		"taint",
		"telemetry",

		// Top-level values
		"_original",
		"affinity",
		"autoscaleBehavior",
		"autoscaleEnabled",
		"autoscaleMax",
		"autoscaleMin",
		"base.enableIstioConfigCRDs",
		"base.validationCABundle",
		"base.validationURL",
		"compatibilityVersion",
		"configMap",
		"cpu.targetAverageUtilization",
		"crlConfigMapName",
		"deploymentAnnotations",
		"deploymentLabels",
		"env",
		"env.CA_TRUSTED_NODE_ACCOUNTS",
		"env.EXTERNAL_CA",
		"env.EXTERNAL_ISTIOD",
		"env.MCS_API_GROUP",
		"env.PILOT_ENABLE_GATEWAY_API_DEPLOYMENT_CONTROLLER",
		"envVarFrom",
		"experimental.stableValidationPolicy",
		"extraContainerArgs",
		"gatewayClasses",

		// Values that require _internal_defaults_do_not_set fallback
		"gateways",
		"gateways.seccompProfile",
		"gateways.securityContext",

		// global.* values
		"global.caAddress",
		"global.caName",
		"global.certSigners",
		"global.configCluster",
		"global.configValidation",
		"global.defaultPodDisruptionBudget",
		"global.defaultPodDisruptionBudget.enabled",
		"global.defaultResources",
		"global.defaultResources.requests",
		"global.defaultResources.requests.cpu",
		"global.externalIstiod",
		"global.hub",
		"global.imagePullPolicy",
		"global.imagePullSecrets",
		"global.ipFamilies",
		"global.ipFamilyPolicy",
		"global.istioNamespace",
		"global.istiod",
		"global.istiod.enableAnalysis",
		"global.logAsJson",
		"global.logging",
		"global.logging.level",
		"global.meshID",
		"global.meshNetworks",
		"global.mountMtlsCerts",
		"global.multiCluster",
		"global.multiCluster.clusterName",
		"global.nativeNftables",
		"global.network",
		"global.networkPolicy",
		"global.networkPolicy.enabled",
		"global.omitSidecarInjectorConfigMap",
		"global.operatorManageWebhooks",
		"global.pilotCertProvider",
		"global.platform",
		"global.priorityClassName",

		// global.proxy.* — these are critical regression paths:
		// global.proxy.tracer and global.proxy.variant are referenced inside a
		// {{define "mesh"}} block and were previously missed because the walker
		// only visited parsed.Tree.Root.
		"global.proxy",
		"global.proxy.autoInject",
		"global.proxy.clusterDomain",
		"global.proxy.componentLogLevel",
		"global.proxy.excludeIPRanges",
		"global.proxy.excludeInboundPorts",
		"global.proxy.excludeOutboundPorts",
		"global.proxy.image",
		"global.proxy.includeIPRanges",
		"global.proxy.includeInboundPorts",
		"global.proxy.includeOutboundPorts",
		"global.proxy.logLevel",
		"global.proxy.outlierLogPath",
		"global.proxy.privileged",
		"global.proxy.readinessFailureThreshold",
		"global.proxy.readinessInitialDelaySeconds",
		"global.proxy.readinessPeriodSeconds",

		// global.proxy.resources — the path from the original bug report.
		// This was missed because value inference didn't expand parent paths.
		// Discovering global.proxy.tracer would not cause global.proxy to be
		// expanded, so sibling values like resources were invisible.
		"global.proxy.resources",
		"global.proxy.resources.limits",
		"global.proxy.resources.limits.cpu",
		"global.proxy.resources.limits.memory",
		"global.proxy.resources.requests",
		"global.proxy.resources.requests.cpu",
		"global.proxy.resources.requests.memory",

		"global.proxy.seccompProfile",
		"global.proxy.startupProbe",
		"global.proxy.startupProbe.enabled",
		"global.proxy.startupProbe.failureThreshold",
		"global.proxy.statusPort",
		"global.proxy.tracer",
		"global.proxy.variant",
		"global.proxy_init",
		"global.proxy_init.forceApplyIptables",
		"global.proxy_init.image",
		"global.remotePilotAddress",
		"global.resourceScope",
		"global.sds",
		"global.sds.token",
		"global.sds.token.aud",
		"global.sts",
		"global.sts.servicePort",
		"global.tag",
		"global.tracer.lightstep.accessToken",
		"global.tracer.lightstep.address",
		"global.trustBundleName",
		"global.variant",
		"global.waypoint",
		"global.waypoint.affinity",
		"global.waypoint.nodeSelector",
		"global.waypoint.resources",
		"global.waypoint.resources.limits",
		"global.waypoint.resources.limits.cpu",
		"global.waypoint.resources.limits.memory",
		"global.waypoint.resources.requests",
		"global.waypoint.resources.requests.cpu",
		"global.waypoint.resources.requests.memory",
		"global.waypoint.tolerations",
		"global.waypoint.topologySpreadConstraints",

		// Remaining top-level values
		"hub",
		"image",
		"initContainers",
		"ipFamilies",
		"ipFamilyPolicy",
		"istiodRemote.enabled",
		"istiodRemote.enabledLocalInjectorIstiod",
		"istiodRemote.injectionCABundle",
		"istiodRemote.injectionPath",
		"istiodRemote.injectionURL",
		"jwksResolverExtraRootCA",
		"keepaliveMaxServerConnectionAge",
		"memory.targetAverageUtilization",
		"meshConfig",
		"meshConfig.defaultProviders",
		"meshConfig.enablePrometheusMerge",
		"meshConfig.rootNamespace",
		"nodeSelector",
		"ownerName",
		"pdb",
		"pdb.maxUnavailable",
		"pdb.minAvailable",
		"pdb.unhealthyPodEvictionPolicy",
		"platform",
		"podAnnotations",
		"podLabels",
		"profile",
		"replicaCount",
		"resources",
		"resources.requests",
		"resources.requests.cpu",
		"resources.requests.memory",
		"revision",
		"revisionTags",
		"rollingMaxSurge",
		"rollingMaxUnavailable",
		"seccompProfile",
		"serviceAccountAnnotations",
		"serviceAnnotations",
		"sidecarInjectorWebhook.alwaysInjectSelector",
		"sidecarInjectorWebhook.defaultTemplates",
		"sidecarInjectorWebhook.enableNamespacesByDefault",
		"sidecarInjectorWebhook.injectedAnnotations",
		"sidecarInjectorWebhook.neverInjectSelector",
		"sidecarInjectorWebhook.reinvocationPolicy",
		"sidecarInjectorWebhook.rewriteAppHTTPProbe",
		"sidecarInjectorWebhook.templates",
		"tag",
		"taint.enabled",
		"taint.namespace",
		"telemetry.enabled",
		"telemetry.v2",
		"telemetry.v2.enabled",
		"telemetry.v2.prometheus",
		"telemetry.v2.prometheus.enabled",
		"telemetry.v2.stackdriver",
		"telemetry.v2.stackdriver.enabled",
		"tolerations",
		"topologySpreadConstraints",
		"traceSampling",
		"trafficDistribution",
		"trustedZtunnelName",
		"trustedZtunnelNamespace",
		"variant",
		"volumeMounts",
		"volumes",
	}

	// Verify all expected paths are present
	var missing []string
	for _, p := range expectedPaths {
		if !discoveredPaths[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("Missing %d expected value paths:\n", len(missing))
		for _, p := range missing {
			t.Errorf("  - %s", p)
		}
	}

	// Verify no unexpected paths are present (catch regressions in either direction)
	expectedSet := make(map[string]bool)
	for _, p := range expectedPaths {
		expectedSet[p] = true
	}
	var unexpected []string
	for _, v := range res.Values {
		if !expectedSet[v.Path] {
			unexpected = append(unexpected, v.Path)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("Found %d unexpected value paths (update expectedPaths if intentional):\n", len(unexpected))
		for _, p := range unexpected {
			t.Errorf("  + %s", p)
		}
	}
}
