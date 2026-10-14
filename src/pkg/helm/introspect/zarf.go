package introspect

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pkgoci "github.com/defenseunicorns/pkg/oci"
	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/api/convert"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/archive"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

// ZarfPackageIntrospection contains the results of introspecting a Zarf package.
type ZarfPackageIntrospection struct {
	APIVersion           string
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

	pkg, err := parseZarfDefinition(zarfBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse zarf.yaml: %w", err)
	}
	packageValues, err := inspectPackageValues(ctx, cacheDir, path, !packageView && isDirectory, pkg.Package, opts.ValuesFiles)
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
			title, include, err := selectPackagedComponent(component, compTarErr == nil, pkg.optionalComponents[component.Name])
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
					filepath.Join(chartsBaseDir, layout.ChartArchiveName(chart.Name, chart.LegacyVersion)),
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
			expandedChartDir := filepath.Join(chartsBaseDir, strings.TrimSuffix(layout.ChartArchiveName(chart.Name, chart.LegacyVersion), ".tgz"))
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
			} else if !packaged && chart.LocalPath() != "" && isDirectory {
				src.LocalPath = filepath.Join(path, chart.LocalPath())
			} else if !packaged && chart.SourceURL() != "" {
				src = remoteChartSource(chart)
			} else {
				if packaged {
					return nil, fmt.Errorf("incomplete selected component %q: chart %q could not be consumed", component.Name, chart.Name)
				}
				continue
			}

			// Keep the declaration order: v1beta1 can interleave plain and
			// templated values files. Runtime templates remain unresolved.
			var valuesFiles []string
			for i, vf := range chart.ValuesFiles {
				location := filepath.Join(valuesBaseDir, layout.ChartValuesFileName(chart.Name, chart.LegacyVersion, i))
				if packaged {
					location = filepath.Join(compExtractDir, filepath.FromSlash(selectedCharts[chartIndex].ValuesPaths[i]))
				} else if _, err := os.Stat(location); err != nil && isDirectory {
					location = filepath.Join(path, vf.Path)
				}
				contents, err := os.ReadFile(location)
				if err != nil {
					return nil, fmt.Errorf("required chart values file %q: %w", location, err)
				}
				if vf.EnableTemplating && bytes.Contains(contents, []byte("{{")) {
					packageValues.Usages = append(packageValues.Usages, inspectTemplateText(string(contents), location, component.Name, chart.Name)...)
				} else {
					// Legacy markers and template-free YAML remain inspectable.
					valuesFiles = append(valuesFiles, location)
				}
			}
			src.ValuesFiles = valuesFiles

			// Introspect
			res, err := IntrospectHelmValues(ctx, src, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to introspect chart %s in component %s: %w", chart.Name, component.Name, err)
			}
			res.PackageAPIVersion = pkg.GetAPIVersion()

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

	finalizePackageValues(&packageValues)
	report := &ZarfPackageIntrospection{
		APIVersion:     pkg.GetAPIVersion(),
		ComponentOrder: componentOrder,
		ChartResults:   results,
		Variables:      convert.PackageToV1alpha1(pkg.Package).Variables,
	}
	if len(packageValues.Files) > 0 || len(packageValues.Schema) > 0 || len(packageValues.Usages) > 0 || len(opts.ValuesFiles) > 0 {
		report.PackageValues = []PackageValues{packageValues}
	}
	syncPackageValueMappings(report)
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

// remoteChartSource preserves the reference form expected by Helm's downloader:
// OCI digests belong in the URL, while tags and repository versions use Version.
func remoteChartSource(chart api.Chart) ChartSource {
	source := ChartSource{
		URL:      chart.SourceURL(),
		RepoName: chart.RepositoryName(),
		Version:  chart.LegacyVersion,
	}
	if source.RepoName == "" {
		source.RepoName = chart.Name
	}
	if chart.HelmRepository != nil {
		source.Version = chart.HelmRepository.Version
	} else if chart.OCI != nil && chart.OCI.Ref != nil {
		if chart.OCI.Ref.Digest != "" {
			source.URL += "@" + chart.OCI.Ref.Digest
			source.Version = ""
		} else {
			source.Version = chart.OCI.Ref.Tag
		}
	}
	return source
}
