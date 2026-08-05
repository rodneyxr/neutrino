package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	pkgoci "github.com/defenseunicorns/pkg/oci"
	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/archive"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

// ZarfPackageIntrospection contains the results of introspecting a Zarf package.
type ZarfPackageIntrospection struct {
	ComponentOrder       []string
	ChartResults         map[string]*IntrospectResult
	Variables            []v1alpha1.InteractiveVariable
	UDSOverrideValues    []UDSOverrideValueWithContext
	UDSOverrideVariables []UDSOverrideVariableWithContext
}

// InspectZarfPackage analyzes a Zarf package to find Helm values.
func InspectZarfPackage(ctx context.Context, path string, version string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	if IsRemote(path) {
		return inspectRemoteZarfPackage(ctx, path, arch, opts)
	}

	var cacheDir string
	var alreadyExtracted bool

	// Check if path is already an extracted directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(path, layout.ZarfYAML)); err == nil {
			cacheDir = path
			alreadyExtracted = true
		}
	}

	// If not already a directory with zarf.yaml, we need to extract it to a cache
	if !alreadyExtracted {
		// Determine persistent cache directory for the extracted package
		cacheKey := path
		if arch != "" {
			cacheKey = fmt.Sprintf("%s-%s", path, arch)
		}
		var err error
		cacheDir, err = getCacheDir("zarf-pkgs", cacheKey, version)
		if err != nil {
			return nil, fmt.Errorf("failed to determine cache dir: %w", err)
		}

		// Ensure cache dir exists
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create cache dir: %w", err)
		}

		// Check if it was extracted previously in our cache
		_, err = os.Stat(filepath.Join(cacheDir, layout.ZarfYAML))
		alreadyExtracted = err == nil
	}

	zarfYamlPath := filepath.Join(cacheDir, layout.ZarfYAML)

	isDirectory := false
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		localZarf := filepath.Join(path, layout.ZarfYAML)
		if _, err := os.Stat(localZarf); err == nil {
			zarfYamlPath = localZarf
			alreadyExtracted = true
			isDirectory = true
		}
	}

	if !isDirectory && (!alreadyExtracted || opts.NoCache) {
		// Clean up if forcing no cache
		if opts.NoCache && alreadyExtracted {
			os.RemoveAll(cacheDir)
			os.MkdirAll(cacheDir, 0755)
		}

		err := archive.Decompress(ctx, path, cacheDir, archive.DecompressOpts{})
		if err != nil {
			return nil, fmt.Errorf("failed to extract zarf package: %w", err)
		}
	}

	// 3. Read zarf.yaml
	zarfBytes, err := os.ReadFile(zarfYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read zarf.yaml: %w", err)
	}

	var pkg v1alpha1.ZarfPackage
	if err := yaml.Unmarshal(zarfBytes, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse zarf.yaml: %w", err)
	}

	results := make(map[string]*IntrospectResult)
	var componentOrder []string

	// 4. Find charts
	for _, component := range pkg.Components {
		// Check for component tarball (components/<name>.tar)
		compTarPath := filepath.Join(cacheDir, layout.ComponentsDir, component.Name+".tar")

		var chartsBaseDir string
		var valuesBaseDir string
		if _, err := os.Stat(compTarPath); err == nil {
			compExtractDir := filepath.Join(cacheDir, "extracted-components", component.Name)
			os.MkdirAll(compExtractDir, 0755)
			err = archive.Decompress(ctx, compTarPath, compExtractDir, archive.DecompressOpts{})
			if err == nil {
				// Check for charts at root of extraction or inside a component-named directory
				chartsBaseDir = filepath.Join(compExtractDir, "charts")
				valuesBaseDir = filepath.Join(compExtractDir, "values")
				if _, err := os.Stat(chartsBaseDir); err != nil {
					chartsBaseDir = filepath.Join(compExtractDir, component.Name, "charts")
					valuesBaseDir = filepath.Join(compExtractDir, component.Name, "values")
				}
			} else {
				return nil, fmt.Errorf("failed to extract component %s: %w", component.Name, err)
			}
		}

		if chartsBaseDir == "" {
			// Fallback: check if it's a directory (legacy or uncompressed layout?)
			// Or check if charts are directly in components/<name>/charts (old layout)
			if isDirectory {
				chartsBaseDir = filepath.Join(path, layout.ComponentsDir, component.Name, "charts")
				valuesBaseDir = filepath.Join(path, layout.ComponentsDir, component.Name, "values")
			} else {
				chartsBaseDir = filepath.Join(cacheDir, layout.ComponentsDir, component.Name, "charts")
				valuesBaseDir = filepath.Join(cacheDir, layout.ComponentsDir, component.Name, "values")
			}
		}

		for _, chart := range component.Charts {
			// Search for the chart file/directory in the component's charts directory
			candidates := []string{
				filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)),
				filepath.Join(chartsBaseDir, chart.Name+".tgz"),
				filepath.Join(chartsBaseDir, chart.Name),
			}

			var chartPath string
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					chartPath = c
					break
				}
			}

			// Expand the chart to a subdirectory so we can point to it
			// chartsBaseDir/<chartName-Version> (expanded)
			expandedChartDir := filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s", chart.Name, chart.Version))
			if chartPath != "" {
				info, _ := os.Stat(chartPath)
				if !info.IsDir() {
					// Archive exists. Expand it to expandedChartDir
					if _, err := FindChartRoot(expandedChartDir); err != nil || opts.NoCache {
						// Clean up before re-expanding if needed
						os.RemoveAll(expandedChartDir)
						archive.Decompress(ctx, chartPath, expandedChartDir, archive.DecompressOpts{})
					}
					// Use expanded path
					if root, err := FindChartRoot(expandedChartDir); err == nil {
						chartPath = root
					}
				}
			}

			// Verify file exists
			var src ChartSource
			if _, err := os.Stat(chartPath); err == nil {
				src.LocalPath = chartPath
			} else if chart.LocalPath != "" && isDirectory {
				src.LocalPath = filepath.Join(path, chart.LocalPath)
			} else if chart.URL != "" {
				src.URL = chart.URL
				src.RepoName = chart.Name
				src.Version = chart.Version
			} else {
				continue
			}

			// Gather Zarf managed values files
			var valuesFiles []string
			managedValuesCount := len(chart.ValuesFiles) + len(chart.TemplatedValuesFiles)
			for i := range managedValuesCount {
				var vf string
				if i < len(chart.ValuesFiles) {
					vf = chart.ValuesFiles[i]
				} else {
					vf = chart.TemplatedValuesFiles[i-len(chart.ValuesFiles)]
				}
				var found bool
				if valuesBaseDir != "" {
					vfPath := filepath.Join(valuesBaseDir, fmt.Sprintf("%s-%s-%d", chart.Name, chart.Version, i))
					if _, err := os.Stat(vfPath); err == nil {
						valuesFiles = append(valuesFiles, vfPath)
						found = true
					}
				}
				if !found && isDirectory {
					vfPath := filepath.Join(path, vf)
					if _, err := os.Stat(vfPath); err == nil {
						valuesFiles = append(valuesFiles, vfPath)
					}
				}
			}
			src.ValuesFiles = valuesFiles

			// Introspect
			res, err := IntrospectHelmValues(ctx, src, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to introspect chart %s in component %s: %w", chart.Name, component.Name, err)
			}

			// Populate default values for Zarf variables
			for i, zv := range res.ZarfVars {
				for _, pkgVar := range pkg.Variables {
					if pkgVar.Name == zv.Name {
						res.ZarfVars[i].DefaultValue = pkgVar.Default
						break
					}
				}
			}

			key := fmt.Sprintf("%s/%s", component.Name, chart.Name)
			results[key] = res
			componentOrder = append(componentOrder, key)
		}
	}

	return &ZarfPackageIntrospection{
		ComponentOrder: componentOrder,
		ChartResults:   results,
		Variables:      pkg.Variables,
	}, nil
}

func inspectRemoteZarfPackage(ctx context.Context, source string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	blobCacheDir, err := getCacheDir("package-blobs", "verified", "")
	if err != nil {
		return nil, fmt.Errorf("failed to determine package reader cache: %w", err)
	}
	platform := pkgoci.PlatformForArch(config.GetArch(arch))
	reader := newPackageReader(newRegistryPackageRemote(source), platform, blobCacheDir, opts.NoCache)
	view, err := reader.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote Zarf package: %w", err)
	}

	viewDir, err := os.MkdirTemp("", "neutrino-package-view-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create package view: %w", err)
	}
	defer os.RemoveAll(viewDir)
	if err := view.materialize(viewDir); err != nil {
		return nil, err
	}
	return InspectZarfPackage(ctx, viewDir, "", arch, opts)
}
