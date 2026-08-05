package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pkgoci "github.com/defenseunicorns/pkg/oci"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type registryPackageRemote struct {
	source string
	remote *pkgoci.OrasRemote
}

func newRegistryPackageRemote(source string) *registryPackageRemote {
	return &registryPackageRemote{source: source}
}

func (r *registryPackageRemote) Resolve(ctx context.Context, platform ocispec.Platform) (packageRoot, error) {
	remote, err := pkgoci.NewOrasRemote(r.source, platform)
	if err != nil {
		return packageRoot{}, err
	}
	r.remote = remote

	descriptor, err := remote.Repo().Resolve(ctx, remote.Repo().Reference.Reference)
	if err != nil {
		return packageRoot{}, err
	}
	if descriptor.MediaType == ocispec.MediaTypeImageIndex || descriptor.MediaType == "application/vnd.docker.distribution.manifest.list.v2+json" {
		indexBytes, err := remote.FetchLayer(ctx, descriptor)
		if err != nil {
			return packageRoot{}, fmt.Errorf("failed to fetch package root index: %w", err)
		}
		var index ocispec.Index
		if err := json.Unmarshal(indexBytes, &index); err != nil {
			return packageRoot{}, fmt.Errorf("failed to parse package root index: %w", err)
		}
		descriptor, err = selectPackagePlatform(index.Manifests, platform)
		if err != nil {
			return packageRoot{}, err
		}
	}

	manifest, err := remote.FetchManifest(ctx, descriptor)
	if err != nil {
		return packageRoot{}, fmt.Errorf("failed to fetch package root manifest %s: %w", descriptor.Digest, err)
	}
	return packageRoot{Descriptor: descriptor, Manifest: manifest.Manifest}, nil
}

func (r *registryPackageRemote) Fetch(ctx context.Context, descriptor ocispec.Descriptor) ([]byte, error) {
	if r.remote == nil {
		return nil, fmt.Errorf("package root must be resolved before fetching descriptors")
	}
	return r.remote.FetchLayer(ctx, descriptor)
}

type platformMismatchError struct {
	requested string
	available []string
}

func (e *platformMismatchError) Error() string {
	return fmt.Sprintf("requested platform %s is unavailable; available platforms: %s", e.requested, strings.Join(e.available, ", "))
}

func selectPackagePlatform(manifests []ocispec.Descriptor, requested ocispec.Platform) (ocispec.Descriptor, error) {
	available := make([]string, 0, len(manifests))
	var selected *ocispec.Descriptor
	for i := range manifests {
		candidate := manifests[i]
		if candidate.Platform == nil {
			available = append(available, "unknown")
			continue
		}
		available = append(available, formatPackagePlatform(*candidate.Platform))
		if candidate.Platform.OS == requested.OS &&
			candidate.Platform.Architecture == requested.Architecture &&
			candidate.Platform.Variant == requested.Variant {
			if selected != nil {
				return ocispec.Descriptor{}, fmt.Errorf("unsupported package root index: multiple manifests match requested platform %s", formatPackagePlatform(requested))
			}
			selected = &candidate
		}
	}
	if selected != nil {
		return *selected, nil
	}

	sort.Strings(available)
	return ocispec.Descriptor{}, &platformMismatchError{
		requested: formatPackagePlatform(requested),
		available: available,
	}
}

func formatPackagePlatform(platform ocispec.Platform) string {
	formatted := platform.OS + "/" + platform.Architecture
	if platform.Variant != "" {
		formatted += "/" + platform.Variant
	}
	return formatted
}
