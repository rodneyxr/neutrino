package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/goccy/go-yaml"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/archive"
	"github.com/zarf-dev/zarf/src/pkg/logger"
)

// UDSBundle represents a minimal structure of uds-bundle.yaml needed for introspection.
type UDSBundle struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name         string `yaml:"name"`
		Description  string `yaml:"description"`
		Version      string `yaml:"version"`
		Architecture string `yaml:"architecture"`
	} `yaml:"metadata"`
	Packages []UDSPackage `yaml:"packages"`
}

type UDSPackage struct {
	Name       string                 `yaml:"name"`
	Repository string                 `yaml:"repository,omitempty"`
	Path       string                 `yaml:"path,omitempty"`
	Ref        string                 `yaml:"ref"`
	Overrides  map[string]interface{} `yaml:"overrides,omitempty"`
}

const UDSBundleMediaType = "application/vnd.uds.config.v1+json"
const UDSBundleFile = "uds-bundle.yaml"

// PullUDSBundle pulls a UDS bundle from a remote source to a temporary directory.
func PullUDSBundle(ctx context.Context, source string, version string, arch string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "neutrino-uds-pull-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	l := logger.From(ctx)
	l.Info(fmt.Sprintf("Pulling UDS Bundle metadata from %s...", source))

	targetArch := config.GetArch(arch)
	platform := oci.PlatformForArch(targetArch)

	remote, err := oci.NewOrasRemote(source, platform)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to create oci remote: %w", err)
	}

	// For a comprehensive pull, we'd pull the whole bundle but we really just need uds-bundle.yaml.
	// But Zarf packages might be inside the OCI registry as well. UDS bundles reference them by repository+ref.
	// So we ONLY need the uds-bundle.yaml to introspect the structure, then we can introspect the individual packages.

	root, err := remote.FetchRoot(ctx)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to fetch root: %w", err)
	}

	var bundleLayer ocispec.Descriptor
	found := false
	for _, layer := range root.Layers {
		if layer.Annotations["org.opencontainers.image.title"] == UDSBundleFile {
			bundleLayer = layer
			found = true
			break
		}
	}

	if !found {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("uds-bundle.yaml not found in OCI manifest")
	}

	bundleBytes, err := remote.FetchLayer(ctx, bundleLayer)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to fetch bundle layer: %w", err)
	}

	bundlePath := filepath.Join(tmpDir, UDSBundleFile)
	if err := os.WriteFile(bundlePath, bundleBytes, 0644); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to write uds-bundle.yaml: %w", err)
	}

	return tmpDir, nil
}

// InspectUDSBundle analyzes a UDS bundle to find Helm values across all its Zarf packages.
func InspectUDSBundle(ctx context.Context, path string, version string, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	l := logger.From(ctx)
	cacheKey := path
	if arch != "" {
		cacheKey = fmt.Sprintf("%s-%s", path, arch)
	}
	cacheDir, err := getCacheDir("uds-bundles", cacheKey, version)
	if err != nil {
		return nil, fmt.Errorf("failed to determine cache dir: %w", err)
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %w", err)
	}

	bundleYamlPath := filepath.Join(cacheDir, UDSBundleFile)
	_, err = os.Stat(bundleYamlPath)
	alreadyExtracted := err == nil

	var tmpDir string

	var remoteOci *oci.OrasRemote
	var remoteRoot *oci.Manifest

	if IsRemote(path) {
		targetArch := config.GetArch(arch)
		platform := oci.PlatformForArch(targetArch)
		if remote, err := oci.NewOrasRemote(path, platform); err == nil {
			remoteOci = remote
			if root, err := remoteOci.FetchRoot(ctx); err == nil {
				remoteRoot = root
			}
		}
	}

	if !alreadyExtracted || opts.NoCache {
		if opts.NoCache && alreadyExtracted {
			os.RemoveAll(cacheDir)
			os.MkdirAll(cacheDir, 0755)
		}

		if IsRemote(path) {
			tmpDir, err = PullUDSBundle(ctx, path, version, arch)
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(tmpDir)

			// Copy from tmpDir to cacheDir
			bytes, err := os.ReadFile(filepath.Join(tmpDir, UDSBundleFile))
			if err != nil {
				return nil, fmt.Errorf("failed to read pulled bundle yaml: %w", err)
			}
			if err := os.WriteFile(bundleYamlPath, bytes, 0644); err != nil {
				return nil, fmt.Errorf("failed to write bundle yaml to cache: %w", err)
			}

		} else {
			// Local tar.zst extraction
			err = archive.Decompress(ctx, path, cacheDir, archive.DecompressOpts{})
			if err != nil {
				return nil, fmt.Errorf("failed to extract uds bundle: %w", err)
			}

			// In a local tar.zst, uds-bundle.yaml is stored as a layer.
			// We need to find it by reading index.json, then manifest, then finding the layer digest.
			// But archive.Decompress for UDS might just extract the blobs.
			// Wait, if it's an OCI layout, we have to parse index.json to find the manifest,
			// then parse the manifest to find the layer with "org.opencontainers.image.title": "uds-bundle.yaml".
			// Let's implement a quick helper for that.
			layerDigest, err := findUdsBundleLayerDigest(cacheDir)
			if err != nil {
				return nil, fmt.Errorf("failed to find uds-bundle.yaml layer in local archive: %w", err)
			}

			blobPath := filepath.Join(cacheDir, "blobs", "sha256", layerDigest)
			blobBytes, err := os.ReadFile(blobPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read uds-bundle.yaml blob: %w", err)
			}

			if err := os.WriteFile(bundleYamlPath, blobBytes, 0644); err != nil {
				return nil, fmt.Errorf("failed to write extracted uds-bundle.yaml: %w", err)
			}
		}
	}

	bundleBytes, err := os.ReadFile(bundleYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read uds-bundle.yaml: %w", err)
	}

	var bundle UDSBundle
	if err := yaml.Unmarshal(bundleBytes, &bundle); err != nil {
		return nil, fmt.Errorf("failed to parse uds-bundle.yaml: %w", err)
	}

	results := make(map[string]*IntrospectResult)
	var componentOrder []string
	var allVariables []v1alpha1.InteractiveVariable

	for _, pkg := range bundle.Packages {
		pkgName := pkg.Name
		var pkgSource string
		var pkgVersion string

		// For local bundles, UDS typically embeds the package as a layer.
		// The ref looks like v0.73.0@sha256:617741de40bcbc92eab7e4a50ced3193bf2c65b9c74de66e6c8bf8402ceb1171
		// If we're inspecting a local bundle (alreadyExtracted or path is local file),
		// we can extract that sha256 blob and treat it as a local Zarf package tar.zst.

		// Provide a way to use the local bundle's extracted OCI layout
		// Because we unpacked the bundle tar.zst to cacheDir, cacheDir is an OCI layout containing multiple manifests.
		// We need to look at cacheDir/index.json to find the manifest for this package.
		// Usually, the annotations in index.json help identify the package.

		layoutPath := ""
		parts := strings.Split(pkg.Ref, "@sha256:")
		if len(parts) == 2 {
			targetLayerDigest := parts[1]

			if IsRemote(path) && remoteOci != nil && remoteRoot != nil {
				foundTarget := false
				var targetLayer ocispec.Descriptor
				for _, layer := range remoteRoot.Layers {
					if layer.Digest.String() == "sha256:"+targetLayerDigest {
						targetLayer = layer
						foundTarget = true
						break
					}
				}

				if foundTarget {
					pkgCacheDir := filepath.Join(cacheDir, "extracted-packages", pkgName)
					os.MkdirAll(pkgCacheDir, 0755)

					l.Info(fmt.Sprintf("Pulling remote OCI layers for embedded package %s...", pkgName))

					manifestData, err := remoteOci.FetchLayer(ctx, targetLayer)
					if err == nil {
						var pkgManifest struct {
							Layers []struct {
								MediaType   string            `json:"mediaType"`
								Digest      string            `json:"digest"`
								Size        int64             `json:"size"`
								Annotations map[string]string `json:"annotations"`
							} `json:"layers"`
						}
						if err := json.Unmarshal(manifestData, &pkgManifest); err != nil {
							l.Debug(fmt.Sprintf("Failed to decode remote pkg manifest json: %s", err))
						} else {
							for _, layer := range pkgManifest.Layers {
								filename, ok := layer.Annotations["org.opencontainers.image.title"]
								if !ok || filename == "" || strings.HasPrefix(filename, "images/") {
									continue
								}

								dstFile := filepath.Join(pkgCacheDir, filename)
								os.MkdirAll(filepath.Dir(dstFile), 0755)

								if _, err := os.Stat(dstFile); err == nil {
									continue
								}

								desc := ocispec.Descriptor{
									MediaType:   layer.MediaType,
									Digest:      digest.Digest(layer.Digest),
									Size:        layer.Size,
									Annotations: layer.Annotations,
								}

								layerData, err := remoteOci.FetchLayer(ctx, desc)
								if err == nil {
									os.WriteFile(dstFile, layerData, 0644)
								} else {
									l.Warn(fmt.Sprintf("Failed to fetch remote layer blob for %s: %s", filename, err))
								}
							}
							layoutPath = pkgCacheDir
							l.Debug(fmt.Sprintf("Successfully reconstructed remote uncompressed package layout for %s at %s", pkgName, layoutPath))
						}
					} else {
						l.Debug(fmt.Sprintf("Failed to fetch manifest layer %s: %s", targetLayerDigest, err))
					}
				} else {
					l.Debug(fmt.Sprintf("Target package digest %s not found in remote bundle layers.", targetLayerDigest))
				}
			} else if !IsRemote(path) {
				indexBytes, err := os.ReadFile(filepath.Join(cacheDir, "index.json"))
				if err == nil {
					var list struct {
						Manifests []struct {
							Digest string `json:"digest"`
						} `json:"manifests"`
					}
					if err := json.Unmarshal(indexBytes, &list); err != nil {
						l.Debug(fmt.Sprintf("Failed to decode index.json: %s", err))
					} else if len(list.Manifests) > 0 {
						bundleManifestHash := list.Manifests[0].Digest[7:]
						bundleManifestPath := filepath.Join(cacheDir, "blobs", "sha256", bundleManifestHash)

						bundleManifestBytes, err := os.ReadFile(bundleManifestPath)
						if err == nil {
							var bundleManifest struct {
								Layers []struct {
									Digest      string            `json:"digest"`
									Annotations map[string]string `json:"annotations"`
								} `json:"layers"`
							}
							if err := json.Unmarshal(bundleManifestBytes, &bundleManifest); err != nil {
								l.Debug(fmt.Sprintf("Failed to decode bundle manifest json: %s", err))
							} else {
								foundTarget := false
								for _, layer := range bundleManifest.Layers {
									if layer.Digest == "sha256:"+targetLayerDigest {
										foundTarget = true
										break
									}
								}

								if foundTarget {
									pkgCacheDir := filepath.Join(cacheDir, "extracted-packages", pkgName)
									os.MkdirAll(pkgCacheDir, 0755)

									manifestPath := filepath.Join(cacheDir, "blobs", "sha256", targetLayerDigest)
									manifestData, err := os.ReadFile(manifestPath)
									if err == nil {
										var pkgManifest struct {
											Layers []struct {
												Digest      string            `json:"digest"`
												Annotations map[string]string `json:"annotations"`
											} `json:"layers"`
										}
										if err := json.Unmarshal(manifestData, &pkgManifest); err != nil {
											l.Debug(fmt.Sprintf("Failed to decode pkg manifest json: %s", err))
										}

										for _, layer := range pkgManifest.Layers {
											if len(layer.Digest) > 7 {
												layerHash := layer.Digest[7:]
												srcBlob := filepath.Join(cacheDir, "blobs", "sha256", layerHash)

												filename, ok := layer.Annotations["org.opencontainers.image.title"]
												if !ok || filename == "" {
													continue
												}

												dstFile := filepath.Join(pkgCacheDir, filename)
												os.MkdirAll(filepath.Dir(dstFile), 0755)

												if _, err := os.Stat(srcBlob); err == nil {
													err = os.Link(srcBlob, dstFile)
													if err != nil && !os.IsExist(err) {
														data, _ := os.ReadFile(srcBlob)
														os.WriteFile(dstFile, data, 0644)
													}
												} else {
													l.Debug(fmt.Sprintf("Missing expected layer blob for %s: %s", filename, srcBlob))
												}
											}
										}
										layoutPath = pkgCacheDir
										l.Debug(fmt.Sprintf("Successfully reconstructed uncompressed package layout for %s at %s", pkgName, layoutPath))
									} else {
										l.Debug(fmt.Sprintf("Manifest path does not exist for target hash %s: %s", targetLayerDigest, manifestPath))
									}
								} else {
									l.Debug(fmt.Sprintf("Target package digest %s not found in bundle layers.", targetLayerDigest))
								}
							}
						}
					}
				}
			}
		}

		if layoutPath != "" {
			pkgSource = layoutPath
			l.Info(fmt.Sprintf("Introspecting UDS Package: %s from local extracted OCI layers", pkgName))
		} else if pkg.Repository != "" {
			// Fall back to remote OCI repository
			pkgSource = fmt.Sprintf("oci://%s", pkg.Repository)
			ref := pkg.Ref
			pkgSource = fmt.Sprintf("%s:%s", pkgSource, ref)
			l.Info(fmt.Sprintf("Introspecting UDS Package: %s from remote %s", pkgName, pkgSource))
		} else if pkg.Path != "" {
			// If there's a local path, and we couldn't extract the OCI layer, fallback to looking for the local file.
			if _, err := os.Stat(pkg.Path); err == nil {
				pkgSource = pkg.Path
			} else {
				// Try to construct a path relative to the bundle
				relPath := filepath.Join(filepath.Dir(path), filepath.Base(pkg.Path))
				if _, err := os.Stat(relPath); err == nil {
					pkgSource = relPath
				} else {
					l.Warn(fmt.Sprintf("Cannot find local package %s at %s. Skipping.", pkgName, pkg.Path))
					continue
				}
			}
		} else {
			l.Warn(fmt.Sprintf("Package %s has no repository or path. Skipping.", pkgName))
			continue
		}

		pkgInspection, err := InspectZarfPackage(ctx, pkgSource, pkgVersion, arch, opts)
		if err != nil {
			l.Warn(fmt.Sprintf("Failed to introspect package %s: %s", pkgName, err.Error()))
			continue
		}

		// Prefix component keys with the package name to distinguish them
		// And preserve their original ordering inside the bundle.
		for _, k := range pkgInspection.ComponentOrder {
			if v, ok := pkgInspection.ChartResults[k]; ok {
				prefixedKey := fmt.Sprintf("%s/%s", pkgName, k)
				results[prefixedKey] = v
				componentOrder = append(componentOrder, prefixedKey)
			}
		}

		// Also grab variables, and perhaps prefix them or at least collect them
		for _, v := range pkgInspection.Variables {
			// Prefix variable name to indicate origin? Or leave as is. Zarf vars are usually unique enough.
			v.Name = fmt.Sprintf("%s.%s", pkgName, v.Name)
			allVariables = append(allVariables, v)
		}
	}

	return &ZarfPackageIntrospection{
		ComponentOrder: componentOrder,
		ChartResults:   results,
		Variables:      allVariables,
	}, nil
}

func findUdsBundleLayerDigest(layoutDir string) (string, error) {
	indexBytes, err := os.ReadFile(filepath.Join(layoutDir, "index.json"))
	if err != nil {
		return "", err
	}

	var index struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}

	if err := yaml.Unmarshal(indexBytes, &index); err != nil {
		return "", err
	}

	if len(index.Manifests) == 0 {
		return "", fmt.Errorf("no manifests found in index.json")
	}

	// Read the first manifest
	manifestDigest := index.Manifests[0].Digest
	// Hash is like sha256:abcd -> we want abcd
	hash := manifestDigest[7:]

	manifestBytes, err := os.ReadFile(filepath.Join(layoutDir, "blobs", "sha256", hash))
	if err != nil {
		return "", err
	}

	var manifest struct {
		Layers []struct {
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"layers"`
	}
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", err
	}

	for _, l := range manifest.Layers {
		if l.Annotations["org.opencontainers.image.title"] == UDSBundleFile {
			return l.Digest[7:], nil
		}
	}

	return "", fmt.Errorf("layer with title uds-bundle.yaml not found")
}
