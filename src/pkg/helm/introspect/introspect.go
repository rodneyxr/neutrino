package introspect

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/pkg/logger"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	v2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"

	"github.com/zarf-dev/zarf/src/pkg/archive"
)

// IntrospectHelmValues loads a chart and analyzes its templates to find overridable values.
func IntrospectHelmValues(ctx context.Context, src ChartSource, opts IntrospectOptions) (*IntrospectResult, error) {
	var c *v2.Chart
	var err error

	l := logger.From(ctx)

	if src.LocalPath != "" {
		l.Debug(fmt.Sprintf("Loading chart from local path: %s", src.LocalPath))
		ch, loadErr := loader.Load(src.LocalPath)
		if loadErr == nil {
			c = ch.(*v2.Chart)
		} else {
			err = loadErr
		}
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
					ch, loadErr := loader.Load(expandedPath)
					if loadErr == nil {
						c = ch.(*v2.Chart)
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
			chartPath, downloadErr := downloadChart(src, destDir)
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
			ch, loadErr := loader.Load(expandedPath)
			if loadErr != nil {
				return nil, fmt.Errorf("failed to load chart: %w", loadErr)
			}
			c = ch.(*v2.Chart)
		}
	} else {
		return nil, fmt.Errorf("no chart source provided")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load chart: %w", err)
	}

	// Merge provided custom values files into the loaded chart's values
	for _, vf := range src.ValuesFiles {
		l.Debug(fmt.Sprintf("Merging values file: %s", vf))
		if vfBytes, readErr := os.ReadFile(vf); readErr == nil {
			var m map[string]interface{}
			if parseErr := yaml.Unmarshal(vfBytes, &m); parseErr == nil {
				c.Values = mergeMaps(c.Values, m)
			} else {
				l.Warn(fmt.Sprintf("Failed to parse values file %s: %s", vf, parseErr))
			}
		} else {
			l.Warn(fmt.Sprintf("Failed to read values file %s: %s", vf, readErr))
		}
	}

	result := &IntrospectResult{
		ChartName:    c.Metadata.Name,
		ChartVersion: c.Metadata.Version,
		Source:       src.LocalPath,
		Values:       []ValueUsage{},
		ZarfVars:     []ZarfVariableUsage{},
	}

	// Detect remote source from metadata
	// Prioritize Sources (list of URLs), then Home
	// Use the first github URL found, or fall back to Home
	for _, s := range c.Metadata.Sources {
		if strings.Contains(s, "github.com") || strings.Contains(s, "gitlab.com") || strings.Contains(s, "bitbucket.org") {
			result.RemoteSource = s
			break
		}
	}
	if result.RemoteSource == "" && len(c.Metadata.Sources) > 0 {
		// Fallback to first source if no known git host found
		result.RemoteSource = c.Metadata.Sources[0]
	}
	if result.RemoteSource == "" && c.Metadata.Home != "" {
		result.RemoteSource = c.Metadata.Home
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

			// Detect Zarf variables in values.yaml
			zarfVars := FindZarfVariables(f.Data, "values.yaml")
			for name, locs := range zarfVars {
				mergeZarfVars(result, name, locs)
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

		// Collect all paths to expand: both the discovered paths and their parent prefixes.
		// For example, if we discovered "global.proxy.tracer", we also want to expand
		// "global.proxy" and "global" so that sibling values like "global.proxy.resources"
		// are also discovered from values.yaml.
		pathsToExpand := make(map[string]string) // path -> chart name
		for _, v := range result.Values {
			pathsToExpand[v.Path] = v.Chart
			// Also add all parent prefixes
			parts := strings.Split(v.Path, ".")
			for i := 1; i < len(parts); i++ {
				parent := strings.Join(parts[:i], ".")
				if _, exists := pathsToExpand[parent]; !exists {
					pathsToExpand[parent] = v.Chart
				}
			}
		}

		for path, chart := range pathsToExpand {
			// Find value in values.yaml
			val := GetValue(c.Values, path)
			if val == nil {
				continue
			}

			// Flatten and find new paths
			subPaths := FlattenToPaths(path, val)
			for _, sub := range subPaths {
				if !existingPaths[sub] {
					// Add inferred value
					newVal := ValueUsage{
						Path:      sub,
						Chart:     chart,
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

	// Build alias map for direct dependencies
	aliasMap := make(map[string]string)
	for _, dep := range c.Metadata.Dependencies {
		if dep.Alias != "" {
			aliasMap[dep.Name] = dep.Alias
		}
	}

	// Populate default values
	for i, v := range result.Values {
		// Try direct path first (handled by GetValue, including _internal fallback)
		val := GetValue(c.Values, v.Path)

		// If not found and it's a subchart, try namespaced path
		if val == nil && v.Chart != c.Metadata.Name {
			prefix := v.Chart
			if alias, ok := aliasMap[v.Chart]; ok {
				prefix = alias
			}
			// Use GetValue again to support fallbacks on the namespaced path too (recursive fallback?)
			// GetValue("crds.foo") -> if "crds.foo" exists, great.
			// If "crds.foo" doesn't exist but "_internal...crds.foo" exists?
			// GetValue wraps logic.
			val = GetValue(c.Values, prefix+"."+v.Path)
		}

		result.Values[i].DefaultValue = val
	}

	l.Debug(fmt.Sprintf("Analysis took %s", time.Since(analyzeStart)))

	return result, nil
}

func downloadChart(src ChartSource, destDir string) (string, error) {
	settings := cli.New()
	settings.RepositoryCache = destDir
	settings.RepositoryConfig = filepath.Join(destDir, "repositories.yaml")

	var regClient *registry.Client
	var err error

	// Handle OCI
	if registry.IsOCI(src.URL) {
		regClient, err = registry.NewClient(
			registry.ClientOptEnableCache(true),
		)
		if err != nil {
			return "", err
		}
	}

	// Set up the chart downloader
	d := downloader.ChartDownloader{
		Out:              io.Discard,
		RegistryClient:   regClient,
		RepositoryCache:  settings.RepositoryCache,
		RepositoryConfig: settings.RepositoryConfig,
		ContentCache:     destDir,
		Verify:           downloader.VerifyNever,
		Getters:          getter.All(settings),
		Options: []getter.Option{
			getter.WithInsecureSkipVerifyTLS(false),
		},
	}

	// If it's a Helm Repo URL (not OCI and doesn't end in .tgz), use the chart name
	ref := src.URL
	if !registry.IsOCI(src.URL) && !strings.HasSuffix(src.URL, ".tgz") && src.RepoName != "" {
		// DownloadTo handles "repo/chart" if the repo is added,
		// or "url/chart" might work in some Helm versions.
		// However, the cleanest way to support random URLs in neutrino
		// is to let d.DownloadTo try its best or we could append the path.
		// For now, let's keep it as url since that's what was there,
		// but we might need to resolve the full URL if DownloadTo fails.
	}

	saved, _, err := d.DownloadTo(ref, src.Version, destDir)
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

func analyzeChart(c *v2.Chart, result *IntrospectResult, opts IntrospectOptions) {
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

func analyzeTemplate(chartName string, t *common.File, result *IntrospectResult) {
	// Parse with Sprig and Helm-like functions to avoid failing on known functions.
	// We don't need actual execution logic, just enough to parse.
	tmpl := template.New(t.Name).Funcs(helmFuncMap())

	parsed, err := tmpl.Parse(string(t.Data))
	if err != nil {
		// Skipped malformed template or one using custom delimiters not supported
		return
	}

	w := newWalker(chartName, t.Name, string(t.Data))

	// Walk all templates in the parsed set, including named templates from {{define}} blocks.
	// Only walking parsed.Tree.Root would miss values referenced inside define blocks.
	for _, assoc := range parsed.Templates() {
		if assoc.Tree != nil && assoc.Tree.Root != nil {
			w.walk(assoc.Tree.Root)
		}
	}

	for _, v := range w.Results {
		mergeUsage(result, v)
	}

	// Scan for Zarf variables in the template
	zarfVars := FindZarfVariables(t.Data, t.Name)
	for name, locs := range zarfVars {
		mergeZarfVars(result, name, locs)
	}
}

func mergeZarfVars(result *IntrospectResult, name string, locs []ValueUsageLocation) {
	for i, v := range result.ZarfVars {
		if v.Name == name {
			result.ZarfVars[i].Locations = append(result.ZarfVars[i].Locations, locs...)
			return
		}
	}
	result.ZarfVars = append(result.ZarfVars, ZarfVariableUsage{
		Name:      name,
		Locations: locs,
	})
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
