package introspect

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/zarf-dev/zarf/src/pkg/archive"
)

// IntrospectHelmValues loads a chart and analyzes its templates to find overridable values.
func IntrospectHelmValues(ctx context.Context, src ChartSource, opts IntrospectOptions) (*IntrospectResult, error) {
	var c *chart.Chart
	var err error

	l := logger.From(ctx)

	if src.LocalPath != "" {
		l.Debug(fmt.Sprintf("Loading chart from local path: %s", src.LocalPath))
		c, err = loader.Load(src.LocalPath)
	} else if src.URL != "" {
		start := time.Now()
		// Determine cache path
		cacheDir, cacheErr := getCacheDir("charts", src.URL, src.Version)
		if cacheErr != nil {
			l.Warn(fmt.Sprintf("Failed to get cache dir: %s", cacheErr))
			// Fallback to temp
			cacheDir = ""
		} else {
			// Ensure cache dir exists
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				l.Warn(fmt.Sprintf("Failed to create cache dir: %s", err))
				cacheDir = ""
			}
		}

		loadedFromCache := false
		if cacheDir != "" && !opts.NoCache {
			matches, _ := filepath.Glob(filepath.Join(cacheDir, "*.tgz"))
			if len(matches) > 0 {
				chartArchive := matches[0]
				l.Debug(fmt.Sprintf("Found chart archive in cache: %s", chartArchive))

				// Ensure it is expanded
				expandedPath, err := ensureExpanded(ctx, cacheDir, chartArchive)
				if err == nil {
					l.Info(fmt.Sprintf("Using cached chart %s:%s", src.URL, src.Version))
					src.LocalPath = expandedPath
					c, err = loader.Load(expandedPath)
					if err == nil {
						loadedFromCache = true
					} else {
						l.Warn(fmt.Sprintf("Failed to load cached chart, redownloading: %s", err))
						os.RemoveAll(cacheDir)
					}
				} else {
					l.Warn(fmt.Sprintf("Failed to expand cached chart: %s", err))
					os.RemoveAll(cacheDir)
				}
			}
		} else if opts.NoCache {
			l.Debug("Skipping cache due to --no-cache")
			// Clean up existing cache if present to force fresh download
			if cacheDir != "" {
				l.Debug(fmt.Sprintf("Cleaning up existing cache: %s", cacheDir))
				os.RemoveAll(cacheDir)
				// Re-create cache dir for the new download
				if err := os.MkdirAll(cacheDir, 0755); err != nil {
					l.Warn(fmt.Sprintf("Failed to recreate cache dir: %s", err))
				}
			}
		}

		if !loadedFromCache {
			destDir := cacheDir
			if destDir == "" {
				tmpDir, downloadErr := os.MkdirTemp("", "neutrino-chart-*")
				if downloadErr != nil {
					return nil, fmt.Errorf("failed to create temp dir: %w", downloadErr)
				}
				defer os.RemoveAll(tmpDir)
				destDir = tmpDir
			}

			l.Info(fmt.Sprintf("Downloading chart %s:%s...", src.URL, src.Version))
			chartPath, downloadErr := downloadChart(src.URL, src.Version, destDir)
			if downloadErr != nil {
				return nil, fmt.Errorf("failed to download chart: %w", downloadErr)
			}
			l.Debug(fmt.Sprintf("Download took %s", time.Since(start)))

			// Expand and use expanded path
			expandedPath, err := ensureExpanded(ctx, destDir, chartPath)
			if err != nil {
				return nil, fmt.Errorf("failed to expand chart: %w", err)
			}

			src.LocalPath = expandedPath
			c, err = loader.Load(expandedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load chart: %w", err)
			}
		}
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

	l.Info(fmt.Sprintf("Analyzing chart templates for %s...", c.Metadata.Name))
	analyzeStart := time.Now()
	analyzeChart(c, result, opts)

	// Try to parse values.yaml for locations
	var valuesIdx map[string]ValueUsageLocation
	for _, f := range c.Raw {
		if f.Name == "values.yaml" {
			result.ValuesFiles = append(result.ValuesFiles, "values.yaml")
			var err error
			valuesIdx, err = ParseValuesLocations(f.Data)
			if err != nil {
				l.Warn(fmt.Sprintf("Failed to parse values.yaml for locations: %s", err))
			}
			break
		}
	}

	// Infer nested values from values.yaml if requested
	if !opts.NoValues {
		l.Debug("Inferring nested paths from values.yaml...")

		// Track existing paths to avoid duplicates
		existingPaths := make(map[string]bool)
		for _, v := range result.Values {
			existingPaths[v.Path] = true
		}

		// Snapshot current values to iterate over
		discovered := make([]ValueUsage, len(result.Values))
		copy(discovered, result.Values)

		for _, v := range discovered {
			// Find value in values.yaml
			val := GetValue(c.Values, v.Path)
			if val == nil {
				continue
			}

			// Flatten and find new paths
			subPaths := FlattenToPaths(v.Path, val)
			for _, sub := range subPaths {
				if !existingPaths[sub] {
					// Add inferred value
					newVal := ValueUsage{
						Path:      sub,
						Chart:     v.Chart,
						Wildcard:  false,
						Locations: []ValueUsageLocation{},
					}

					// Add location if available
					if loc, ok := valuesIdx[sub]; ok {
						newVal.Locations = append(newVal.Locations, loc)
					}

					result.Values = append(result.Values, newVal)
					existingPaths[sub] = true
				}
			}
		}
	}

	// Populate default values
	for i, v := range result.Values {
		// Use cached values if available, otherwise just use what chart has
		// Note: c.Values are map[string]interface{}
		result.Values[i].DefaultValue = GetValue(c.Values, v.Path)
	}

	l.Debug(fmt.Sprintf("Analysis took %s", time.Since(analyzeStart)))

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

// ensureExpanded extracts the chart archive if not already expanded.
// Returns the path to the expanded chart directory.
func ensureExpanded(ctx context.Context, destDir string, archivePath string) (string, error) {
	// Check if already expanded (try to find root)
	if root, err := FindChartRoot(destDir); err == nil {
		return root, nil
	}

	// Not found, extract
	if err := archive.Decompress(ctx, archivePath, destDir, archive.DecompressOpts{}); err != nil {
		return "", err
	}

	// Find it again
	if root, err := FindChartRoot(destDir); err == nil {
		return root, nil
	}

	return "", fmt.Errorf("extraction succeeded but no chart found in %s", destDir)
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
