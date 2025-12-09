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
	// If path is remote, pull it first
	if isRemote(path) {
		localPath, err := PullZarfPackage(ctx, path, version, arch)

		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(filepath.Dir(localPath)) // Cleanup the temp dir created by PullZarfPackage
		path = localPath
	}

	// 1. Create temp directory for extraction
	tmpDir, err := os.MkdirTemp("", "neutrino-zarf-pkg-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 2. Extract zarf.yaml
	// We handle .tar.zst using Zarf's archive package
	err = archive.Decompress(context.TODO(), path, tmpDir, archive.DecompressOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to extract zarf package: %w", err)
	}

	// 3. Read zarf.yaml
	zarfYamlPath := filepath.Join(tmpDir, layout.ZarfYAML)
	zarfBytes, err := os.ReadFile(zarfYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read zarf.yaml: %w", err)
	}

	var pkg v1alpha1.ZarfPackage
	if err := yaml.Unmarshal(zarfBytes, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse zarf.yaml: %w", err)
	}

	results := make(map[string]*IntrospectResult)

	// 4. Find charts
	for _, component := range pkg.Components {
		// Check for component tarball (components/<name>.tar)
		compTarPath := filepath.Join(tmpDir, layout.ComponentsDir, component.Name+".tar")

		// If tarball exists, extract it
		var chartsBaseDir string
		if _, err := os.Stat(compTarPath); err == nil {
			compExtractDir := filepath.Join(tmpDir, "extracted-components", component.Name)
			if err := archive.Decompress(context.TODO(), compTarPath, compExtractDir, archive.DecompressOpts{}); err != nil {
				return nil, fmt.Errorf("failed to extract component %s: %w", component.Name, err)
			}
			// Charts are usually at: <extract-dir>/<component-name>/charts/
			chartsBaseDir = filepath.Join(compExtractDir, component.Name, "charts")
		} else {
			// Fallback: check if it's a directory (legacy or uncompressed layout?)
			// Or check if charts are directly in components/<name>/charts (old layout)
			chartsBaseDir = filepath.Join(tmpDir, layout.ComponentsDir, component.Name, "charts")
		}

		for _, chart := range component.Charts {
			chartFileName := fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)
			chartPath := filepath.Join(chartsBaseDir, chartFileName)

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

			key := fmt.Sprintf("%s/%s", component.Name, chart.Name)
			results[key] = res
		}
	}

	return &ZarfPackageIntrospection{
		ChartResults: results,
		Variables:    pkg.Variables,
	}, nil
}

func isRemote(source string) bool {
	return len(source) > 6 && (source[:6] == "oci://" || source[:7] == "http://" || source[:8] == "https://")
}
