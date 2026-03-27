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

func PullZarfPackage(ctx context.Context, source string, version string, arch string, destDir string) (string, error) {

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

	pkgPath, err := packager.Pull(ctx, source, destDir, pullOpts)
	if err != nil {
		return "", fmt.Errorf("failed to pull zarf package: %w", err)
	}

	return pkgPath, nil
}

// ZarfPackageIntrospection contains the results of introspecting a Zarf package.
type ZarfPackageIntrospection struct {
	ComponentOrder []string
	ChartResults   map[string]*IntrospectResult
	Variables      []v1alpha1.InteractiveVariable
}

// InspectZarfPackage analyzes a Zarf package to find Helm values.
func InspectZarfPackage(ctx context.Context, path string, version string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
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

	if !alreadyExtracted || opts.NoCache {
		// Clean up if forcing no cache
		if opts.NoCache && alreadyExtracted {
			os.RemoveAll(cacheDir)
			os.MkdirAll(cacheDir, 0755)
		}

		// Pull if remote
		localTarPath := path
		if IsRemote(path) {
			tmpDir, err := os.MkdirTemp("", "neutrino-zarf-pull-*")
			if err != nil {
				return nil, fmt.Errorf("failed to create temp dir: %w", err)
			}
			defer os.RemoveAll(tmpDir)

			pulledPath, err := PullZarfPackage(ctx, path, version, arch, tmpDir)
			if err != nil {
				return nil, err
			}
			localTarPath = pulledPath
		}

		// Extract to cacheDir
		err := archive.Decompress(context.TODO(), localTarPath, cacheDir, archive.DecompressOpts{})
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

		// If tarball exists, extract it to a persistent location inside cacheDir
		var chartsBaseDir string
		if _, err := os.Stat(compTarPath); err == nil {
			compExtractDir := filepath.Join(cacheDir, "extracted-components", component.Name)
			// Only extract if not already extracted ??
			// For simplicity, we can rely on Decompress overwriting or checking.
			// Ideally we check if charts dir exists.

			if err := archive.Decompress(context.TODO(), compTarPath, compExtractDir, archive.DecompressOpts{}); err != nil {
				return nil, fmt.Errorf("failed to extract component %s: %w", component.Name, err)
			}
			// Charts are usually at: <extract-dir>/<component-name>/charts/
			chartsBaseDir = filepath.Join(compExtractDir, component.Name, "charts")
		} else {
			// Fallback: check if it's a directory (legacy or uncompressed layout?)
			// Or check if charts are directly in components/<name>/charts (old layout)
			chartsBaseDir = filepath.Join(cacheDir, layout.ComponentsDir, component.Name, "charts")
		}

		for _, chart := range component.Charts {
			chartFileName := fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)
			chartPath := filepath.Join(chartsBaseDir, chartFileName)

			// Expand the chart to a subdirectory so we can point to it
			// chartsBaseDir/<chartName-Version> (expanded)
			expandedChartDir := filepath.Join(chartsBaseDir, fmt.Sprintf("%s-%s", chart.Name, chart.Version))
			if _, err := os.Stat(chartPath); err == nil {
				// Archive exists. Expand it to expandedChartDir
				// Check if already expanded?
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

			// Verify file exists
			if _, err := os.Stat(chartPath); err != nil {
				// If not found, log/skip
				continue
			}

			// Introspect
			res, err := IntrospectHelmValues(ctx, ChartSource{LocalPath: chartPath}, opts)
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

