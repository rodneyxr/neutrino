package introspect

import (
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
)

// IntrospectHelmValues loads a chart and analyzes its templates to find overridable values.
func IntrospectHelmValues(src ChartSource, opts IntrospectOptions) (*IntrospectResult, error) {
	var c *chart.Chart
	var err error

	if src.LocalPath != "" {
		c, err = loader.Load(src.LocalPath)
	} else if src.URL != "" {
		tmpDir, downloadErr := os.MkdirTemp("", "neutrino-chart-*")
		if downloadErr != nil {
			return nil, fmt.Errorf("failed to create temp dir: %w", downloadErr)
		}
		defer os.RemoveAll(tmpDir)

		chartPath, downloadErr := downloadChart(src.URL, src.Version, tmpDir)
		if downloadErr != nil {
			return nil, fmt.Errorf("failed to download chart: %w", downloadErr)
		}
		src.LocalPath = chartPath // Update source to the downloaded path for reporting
		c, err = loader.Load(chartPath)
	} else {
		return nil, fmt.Errorf("no chart source provided")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load chart: %w", err)
	}

	result := &IntrospectResult{
		ChartName:    c.Metadata.Name,
		ChartVersion: c.Metadata.Version,
		Source:       src.LocalPath,
		Values:       []ValueUsage{},
	}

	analyzeChart(c, result, opts)

	return result, nil
}

func downloadChart(url string, version string, destDir string) (string, error) {
	settings := cli.New()

	var regClient *registry.Client
	var err error

	// Handle OCI
	if registry.IsOCI(url) {
		regClient, err = registry.NewClient(
			registry.ClientOptEnableCache(true),
		)
		if err != nil {
			return "", err
		}
	}

	// Set up the chart downloader
	d := downloader.ChartDownloader{
		Out:            io.Discard,
		RegistryClient: regClient,
		Verify:         downloader.VerifyNever,
		Getters:        getter.All(settings),
		Options: []getter.Option{
			getter.WithInsecureSkipVerifyTLS(false),
		},
	}

	saved, _, err := d.DownloadTo(url, version, destDir)
	if err != nil {
		return "", fmt.Errorf("downloader failed: %w", err)
	}

	return saved, nil
}

func analyzeChart(c *chart.Chart, result *IntrospectResult, opts IntrospectOptions) {
	for _, t := range c.Templates {
		// Include hooks if requested or default (ADR says include by default)
		// We process everything in templates/
		analyzeTemplate(c.Metadata.Name, t, result)
	}

	if opts.IncludeSubcharts {
		for _, dep := range c.Dependencies() {
			analyzeChart(dep, result, opts)
		}
	}
}

func analyzeTemplate(chartName string, t *chart.File, result *IntrospectResult) {
	// Parse with Sprig and Helm-like functions to avoid failing on known functions.
	// We don't need actual execution logic, just enough to parse.
	tmpl := template.New(t.Name).Funcs(helmFuncMap())

	parsed, err := tmpl.Parse(string(t.Data))
	if err != nil {
		// Skipped malformed template or one using custom delimiters not supported
		return
	}

	w := newWalker(chartName, t.Name, string(t.Data))
	if parsed.Tree != nil && parsed.Tree.Root != nil {
		w.walk(parsed.Tree.Root)
	}

	for _, v := range w.Results {
		mergeUsage(result, v)
	}
}

func mergeUsage(result *IntrospectResult, usage ValueUsage) {
	for i, existing := range result.Values {
		if existing.Path == usage.Path && existing.Chart == usage.Chart {
			result.Values[i].Locations = append(result.Values[i].Locations, usage.Locations...)
			return
		}
	}
	result.Values = append(result.Values, usage)
}

func helmFuncMap() template.FuncMap {
	f := sprig.TxtFuncMap()
	// Add common Helm functions that are not in Sprig
	f["include"] = func(string, interface{}) string { return "" }
	f["required"] = func(string, interface{}) (interface{}, error) { return nil, nil }
	f["tpl"] = func(string, interface{}) string { return "" }
	f["toYaml"] = func(interface{}) string { return "" }
	f["fromYaml"] = func(string) map[string]interface{} { return nil }
	f["fromYamlArray"] = func(string) []interface{} { return nil }
	f["toJson"] = func(interface{}) string { return "" }
	f["fromJson"] = func(string) map[string]interface{} { return nil }
	f["fromJsonArray"] = func(string) []interface{} { return nil }
	f["lookup"] = func(string, string, string, string) map[string]interface{} { return nil }
	return f
}
