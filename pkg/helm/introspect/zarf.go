package introspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/archive"
	"github.com/zarf-dev/zarf/src/pkg/packager"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

// PullZarfPackage pulls a Zarf package from a remote source (OCI or HTTP) to the temp directory.
func PullZarfPackage(ctx context.Context, source string, version string, arch string) (string, error) {
	// Create temp directory for the package file
	tmpDir, err := os.MkdirTemp("", "neutrino-zarf-pull-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	// Caller responsible for cleanup or it happens when main exits (OS temp)
	// But better if we could clean up. For now, it returns the file path.

	// Determine cache path
	cachePath, err := config.GetAbsHomePath(config.ZarfDefaultCachePath)
	if err != nil {
		// Fallback to temp or just log? simpler to error out or use raw defaults if expansion fails (unlikely)
		return "", fmt.Errorf("failed to get absolute home path: %w", err)
	}

	pullOpts := packager.PullOptions{
		CachePath:    cachePath,
		Architecture: arch,
	}

	pkgPath, err := packager.Pull(ctx, source, tmpDir, pullOpts)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to pull zarf package: %w", err)
	}

	return pkgPath, nil
}

// ZarfPackageIntrospection contains the results of introspecting a Zarf package.
type ZarfPackageIntrospection struct {
	ChartResults map[string]*IntrospectResult
	Variables    []v1alpha1.InteractiveVariable
}

// InspectZarfPackage analyzes a Zarf package to find Helm values.
func InspectZarfPackage(ctx context.Context, path string, version string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	// 1. Determine if we are working with a local directory or a remote/compressed package
	var packageDir string
	var cleanup func()

	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		// Local directory case
		packageDir = path
		cleanup = func() {} // No cleanup needed for local dir
	} else {
		// Compressed or remote case
		// Determine persistent cache directory for the extracted package
		cacheKey := path
		if arch != "" {
			cacheKey = fmt.Sprintf("%s-%s", path, arch)
		}
		cacheDir, err := getCacheDir("zarf-pkgs", cacheKey, version)
		if err != nil {
			return nil, fmt.Errorf("failed to determine cache dir: %w", err)
		}

		// Ensure cache dir exists
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create cache dir: %w", err)
		}

		packageDir = cacheDir
		cleanup = func() {} // Default no cleanup (cache)

		// Check if already extracted (look for zarf.yaml)
		zarfYamlPath := filepath.Join(cacheDir, layout.ZarfYAML)
		_, err = os.Stat(zarfYamlPath)
		alreadyExtracted := err == nil

		if !alreadyExtracted || opts.NoCache {
			// Clean up if forcing no cache
			if opts.NoCache && alreadyExtracted {
				os.RemoveAll(cacheDir)
				os.MkdirAll(cacheDir, 0755)
			}

			// Pull if remote
			localTarPath := path
			if isRemote(path) {
				pulledPath, err := PullZarfPackage(ctx, path, version, arch)
				if err != nil {
					return nil, err
				}
				localTarPath = pulledPath
				// Clean up the pulled tarball after extraction
				defer os.Remove(localTarPath)
			}

			// Extract to cacheDir
			err = archive.Decompress(context.TODO(), localTarPath, cacheDir, archive.DecompressOpts{})
			if err != nil {
				return nil, fmt.Errorf("failed to extract zarf package: %w", err)
			}
		}
	}
	defer cleanup()

	// 2. Read zarf.yaml
	zarfYamlPath := filepath.Join(packageDir, layout.ZarfYAML)
	zarfBytes, err := os.ReadFile(zarfYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read zarf.yaml: %w", err)
	}

	var pkg v1alpha1.ZarfPackage
	if err := yaml.Unmarshal(zarfBytes, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse zarf.yaml: %w", err)
	}

	results := make(map[string]*IntrospectResult)

	// Helper to process components recursively
	var processComponents func(currentDir string, components []v1alpha1.ZarfComponent) error
	processComponents = func(currentDir string, components []v1alpha1.ZarfComponent) error {
		for _, component := range components {
			// Handle imports first
			if component.Import.Path != "" {
				// Resolve import path
				importPath := component.Import.Path
				if !filepath.IsAbs(importPath) {
					importPath = filepath.Join(currentDir, importPath)
				}

				// Verify import path exists
				if _, err := os.Stat(importPath); err != nil {
					// Warn but continue? Or error? Zarf would error.
					// Let's try to proceed if possible or just log error?
					// For now, return error as strict behavior.
					return fmt.Errorf("imported component path not found: %s", importPath)
				}

				// Read imported zarf.yaml
				importedYamlPath := filepath.Join(importPath, layout.ZarfYAML)
				importedBytes, err := os.ReadFile(importedYamlPath)
				if err != nil {
					return fmt.Errorf("failed to read imported zarf.yaml at %s: %w", importedYamlPath, err)
				}
				var importedPkg v1alpha1.ZarfPackage
				if err := yaml.Unmarshal(importedBytes, &importedPkg); err != nil {
					return fmt.Errorf("failed to parse imported zarf.yaml at %s: %w", importedYamlPath, err)
				}

				// Recurse
				if err := processComponents(importPath, importedPkg.Components); err != nil {
					return err
				}
				continue
			}

			// Handle charts
			for _, chart := range component.Charts {
				var chartPath string

				// 1. Check for localPath (used in local development)
				if chart.LocalPath != "" {
					resolvedPath := chart.LocalPath
					if !filepath.IsAbs(resolvedPath) {
						resolvedPath = filepath.Join(currentDir, resolvedPath)
					}
					if _, err := os.Stat(resolvedPath); err == nil {
						chartPath = resolvedPath
					}
				}

				// 2. Fallback to package components directory (legacy/built package)
				if chartPath == "" {
					// Check for component tarball (components/<name>.tar)
					// This logic usually only applies to the root packageDir, not imported ones usually?
					// Actually, if we are in a built package, imports are flattened or handled differently.
					// If we are inspecting a built package, currentDir is the extracted root.
					// If we are inspecting a source directory, we rely on LocalPath.

					// If we are in the root packageDir (built package case)
					// Verify if we can find the chart in the standard layout

					// Only try standard layout if currentDir is the root packageDir we started with
					// (Simple assumption for now: imports in source mode won't use tarballs)
					if currentDir == packageDir {
						compTarPath := filepath.Join(packageDir, layout.ComponentsDir, component.Name+".tar")

						// If tarball exists, extract it
						if _, err := os.Stat(compTarPath); err == nil {
							compExtractDir := filepath.Join(packageDir, "extracted-components", component.Name)
							if err := archive.Decompress(context.TODO(), compTarPath, compExtractDir, archive.DecompressOpts{}); err == nil {
								// Charts are usually at: <extract-dir>/<component-name>/charts/
								expandedChartDir := filepath.Join(compExtractDir, component.Name, "charts", fmt.Sprintf("%s-%s", chart.Name, chart.Version))

								// Expand the chart tarball found there
								chartTarball := filepath.Join(compExtractDir, component.Name, "charts", fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version))
								if _, err := os.Stat(chartTarball); err == nil {
									// Extract chart tgz
									if _, err := FindChartRoot(expandedChartDir); err != nil || opts.NoCache {
										os.RemoveAll(expandedChartDir)
										archive.Decompress(ctx, chartTarball, expandedChartDir, archive.DecompressOpts{})
									}
									if root, err := FindChartRoot(expandedChartDir); err == nil {
										chartPath = root
									}
								}
							}
						} else {
							// Check uncompressed/legacy layout
							// components/<name>/charts/<chart>.tgz
							chartsBaseDir := filepath.Join(packageDir, layout.ComponentsDir, component.Name, "charts")
							chartFileName := fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)
							chartTarball := filepath.Join(chartsBaseDir, chartFileName)

							if _, err := os.Stat(chartTarball); err == nil {
								expandedChartDir := filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s", chart.Name, chart.Version))
								if _, err := FindChartRoot(expandedChartDir); err != nil || opts.NoCache {
									os.RemoveAll(expandedChartDir)
									archive.Decompress(ctx, chartTarball, expandedChartDir, archive.DecompressOpts{})
								}
								if root, err := FindChartRoot(expandedChartDir); err == nil {
									chartPath = root
								}
							}
						}
					}
				}

				if chartPath == "" {
					// Skip if not found
					continue
				}

				// Introspect
				res, err := IntrospectHelmValues(ctx, ChartSource{LocalPath: chartPath}, opts)
				if err != nil {
					return fmt.Errorf("failed to introspect chart %s in component %s: %w", chart.Name, component.Name, err)
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
			}
		}
		return nil
	}

	// Start processing from root
	if err := processComponents(packageDir, pkg.Components); err != nil {
		return nil, err
	}

	return &ZarfPackageIntrospection{
		ChartResults: results,
		Variables:    pkg.Variables,
	}, nil
}

func isRemote(source string) bool {
	return len(source) > 6 && (source[:6] == "oci://" || source[:7] == "http://" || source[:8] == "https://")
}
