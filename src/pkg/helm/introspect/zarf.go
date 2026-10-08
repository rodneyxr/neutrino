package introspect

import (
	"bytes"
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
	PackageValues        []PackageValues
	Bundle               *BundleMetadata
}

// InspectZarfPackage analyzes a Zarf package to find Helm values.
func InspectZarfPackage(ctx context.Context, path string, version string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	if IsRemote(path) {
		return inspectRemoteZarfPackage(ctx, path, arch, opts)
	}
	return inspectLocalZarfPackage(ctx, path, version, arch, opts, false)
}

func inspectLocalZarfPackage(ctx context.Context, path string, version string, arch string, opts IntrospectOptions, packageView bool) (*ZarfPackageIntrospection, error) {

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
	packageValues, err := inspectPackageValues(ctx, cacheDir, path, !packageView && isDirectory, pkg, opts.ValuesFiles)
	if err != nil {
		return nil, err
	}

	results := make(map[string]*IntrospectResult)
	var componentOrder []string

	// 4. Find charts
	for _, component := range pkg.Components {
		compTarPath := filepath.Join(cacheDir, layout.ComponentsDir, component.Name+".tar")
		_, compTarErr := os.Stat(compTarPath)
		packaged := packageView || !isDirectory || compTarErr == nil
		var selectedCharts []packagedChart
		var compExtractDir string
		var chartsBaseDir, valuesBaseDir string
		if packaged && len(component.Charts) > 0 {
			title, include, err := selectPackagedComponent(component, compTarErr == nil)
			if err != nil {
				return nil, err
			}
			if !include {
				continue
			}
			contents, err := os.ReadFile(compTarPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read selected component %q: %w", title, err)
			}
			entries, err := readComponentEntries(title, contents)
			if err != nil {
				return nil, err
			}
			selectedCharts, err = packagedChartLayout(component, title, entries)
			if err != nil {
				return nil, err
			}
			compExtractDir = filepath.Join(cacheDir, "extracted-components", component.Name)
			// Always rebuild from the selected archive; stale files must not
			// complete a different Package Snapshot.
			if err := os.RemoveAll(compExtractDir); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(compExtractDir, 0755); err != nil {
				return nil, err
			}
			if err := archive.Decompress(ctx, compTarPath, compExtractDir, archive.DecompressOpts{}); err != nil {
				return nil, fmt.Errorf("failed to extract component %s: %w", component.Name, err)
			}
		} else {
			chartsBaseDir = filepath.Join(cacheDir, layout.ComponentsDir, component.Name, "charts")
			valuesBaseDir = filepath.Join(cacheDir, layout.ComponentsDir, component.Name, "values")
		}

		for chartIndex, chart := range component.Charts {
			var chartPath string
			if packaged {
				chartPath = filepath.Join(compExtractDir, filepath.FromSlash(selectedCharts[chartIndex].ChartPath))
				chartsBaseDir = filepath.Dir(chartPath)
			} else {
				for _, candidate := range []string{
					filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)),
					filepath.Join(chartsBaseDir, chart.Name+".tgz"),
					filepath.Join(chartsBaseDir, chart.Name),
				} {
					if _, err := os.Stat(candidate); err == nil {
						chartPath = candidate
						break
					}
				}
			}

			// Expand the chart to a subdirectory so we can point to it
			// chartsBaseDir/<chartName-Version> (expanded)
			expandedChartDir := filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s", chart.Name, chart.Version))
			if chartPath != "" {
				info, err := os.Stat(chartPath)
				if err != nil {
					return nil, fmt.Errorf("failed to consume chart %s in component %s: %w", chart.Name, component.Name, err)
				}
				if !info.IsDir() {
					// Archive exists. Expand it to expandedChartDir
					if _, err := FindChartRoot(expandedChartDir); err != nil || opts.NoCache {
						// Clean up before re-expanding if needed
						if err := os.RemoveAll(expandedChartDir); err != nil {
							return nil, err
						}
						if err := archive.Decompress(ctx, chartPath, expandedChartDir, archive.DecompressOpts{}); err != nil {
							return nil, fmt.Errorf("failed to extract chart %s in component %s: %w", chart.Name, component.Name, err)
						}
					}
					// Use expanded path
					root, err := FindChartRoot(expandedChartDir)
					if err != nil {
						return nil, fmt.Errorf("failed to find chart %s in component %s: %w", chart.Name, component.Name, err)
					}
					chartPath = root
				}
			}

			// Verify file exists
			var src ChartSource
			if _, err := os.Stat(chartPath); err == nil {
				src.LocalPath = chartPath
			} else if !packaged && chart.LocalPath != "" && isDirectory {
				src.LocalPath = filepath.Join(path, chart.LocalPath)
			} else if !packaged && chart.URL != "" {
				src.URL = chart.URL
				src.RepoName = chart.Name
				src.Version = chart.Version
			} else {
				if packaged {
					return nil, fmt.Errorf("incomplete selected component %q: chart %q could not be consumed", component.Name, chart.Name)
				}
				continue
			}

			// Only static chart values files are YAML. Templated files are inspected
			// as unresolved runtime inputs, never passed to Helm's YAML parser.
			var valuesFiles []string
			for i, vf := range chart.ValuesFiles {
				if packaged {
					valuesFiles = append(valuesFiles, filepath.Join(compExtractDir, filepath.FromSlash(selectedCharts[chartIndex].ValuesPaths[i])))
					continue
				}
				if valuesBaseDir != "" {
					vfPath := filepath.Join(valuesBaseDir, fmt.Sprintf("%s-%s-%d", chart.Name, chart.Version, i))
					if _, err := os.Stat(vfPath); err == nil {
						valuesFiles = append(valuesFiles, vfPath)
						continue
					}
				}
				if isDirectory {
					vfPath := filepath.Join(path, vf)
					if _, err := os.Stat(vfPath); err == nil {
						valuesFiles = append(valuesFiles, vfPath)
					}
				}
			}
			for i, vf := range chart.TemplatedValuesFiles {
				location := fmt.Sprintf("%s-%s-%d", chart.Name, chart.Version, len(chart.ValuesFiles)+i)
				if packaged {
					location = filepath.Join(compExtractDir, filepath.FromSlash(selectedCharts[chartIndex].ValuesPaths[len(chart.ValuesFiles)+i]))
				}
				if valuesBaseDir != "" {
					location = filepath.Join(valuesBaseDir, location)
				}
				if _, err := os.Stat(location); err != nil && isDirectory {
					location = filepath.Join(path, vf)
				}
				contents, err := os.ReadFile(location)
				if err != nil {
					return nil, fmt.Errorf("required templated values file %q: %w", location, err)
				}
				if !bytes.Contains(contents, []byte("{{")) {
					// Legacy marker substitution is static YAML and remains
					// inspectable even when listed as a templated values file.
					valuesFiles = append(valuesFiles, location)
				} else {
					packageValues.Usages = append(packageValues.Usages, inspectTemplateText(string(contents), location, component.Name, chart.Name)...)
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
			for _, mapping := range chart.Values {
				usage := PackageValueUsage{
					Path: mapping.SourcePath, ComponentName: component.Name,
					ChartName: chart.Name, TargetPath: mapping.TargetPath,
					ExcludePaths: mapping.ExcludePaths,
				}
				packageValues.Usages = append(packageValues.Usages, usage)
				res.PackageValueMappings = append(res.PackageValueMappings, usage)
			}

			key := fmt.Sprintf("%s/%s", component.Name, chart.Name)
			results[key] = res
			componentOrder = append(componentOrder, key)
		}
		packageValues.Usages = append(packageValues.Usages, inspectComponentRuntime(component, cacheDir, path, isDirectory)...)
	}

	report := &ZarfPackageIntrospection{
		ComponentOrder: componentOrder,
		ChartResults:   results,
		Variables:      pkg.Variables,
	}
	if len(packageValues.Files) > 0 || len(packageValues.Schema) > 0 || len(packageValues.Usages) > 0 || len(opts.ValuesFiles) > 0 {
		report.PackageValues = []PackageValues{packageValues}
	}
	return report, nil
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

	viewDir, err := getPackageViewDir(source, view.Snapshot.RootDigest.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create package view: %w", err)
	}
	if err := view.materialize(viewDir); err != nil {
		return nil, err
	}
	return inspectLocalZarfPackage(ctx, viewDir, "", arch, opts, true)
}
