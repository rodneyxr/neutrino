package introspect

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/klauspost/compress/zstd"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/zarf-dev/zarf/src/config"
)

func isNextLocalOrRemote(ctx context.Context, source, arch string) (bool, error) {
	if IsRemote(source) {
		return isNextRemote(ctx, source, arch)
	}
	if _, err := os.Stat(source); err != nil {
		return false, nil
	}
	return isNextLocal(source)
}
func isNextLocal(source string) (bool, error) {
	info, err := os.Stat(source)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(source, nextBundleFile)); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
		index, err := nextReadControl(filepath.Join(source, "oci", "index.json"))
		if err == nil {
			var idx ocispec.Index
			if err := json.Unmarshal(index, &idx); err != nil {
				return false, err
			}
			return idx.ArtifactType == nextBundleType, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
		return false, nil
	}
	if filepath.Base(source) == nextBundleFile {
		return true, nil
	}
	if !strings.HasSuffix(source, ".tar.zst") {
		return false, nil
	}
	file, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer file.Close()
	zr, err := zstd.NewReader(file)
	if err != nil {
		return false, fmt.Errorf("read archive %q: %w", source, err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, fmt.Errorf("scan archive %q: %w", source, err)
		}
		if strings.TrimPrefix(hdr.Name, "./") == "oci/index.json" {
			if hdr.Size > nextMaxControlBytes {
				return false, fmt.Errorf("oversized bundle OCI index")
			}
			data, err := io.ReadAll(io.LimitReader(tr, nextMaxControlBytes+1))
			if err != nil {
				return false, err
			}
			var idx ocispec.Index
			if err := json.Unmarshal(data, &idx); err != nil {
				return false, fmt.Errorf("parse bundle OCI index: %w", err)
			}
			return idx.ArtifactType == nextBundleType, nil
		}
		if hdr.Name == "index.json" {
			return false, nil
		}
	}
	return false, nil
}
func isNextRemote(ctx context.Context, source, arch string) (bool, error) {
	remote, err := oci.NewOrasRemote(source, oci.PlatformForArch(config.GetArch(arch)))
	if err != nil {
		return false, err
	}
	descriptor, err := remote.Repo().Resolve(ctx, remote.Repo().Reference.Reference)
	if err != nil {
		return false, err
	}
	if descriptor.MediaType != ocispec.MediaTypeImageIndex {
		return false, nil
	}
	bytes, err := nextFetch(ctx, remote.FetchLayer, descriptor)
	if err != nil {
		return false, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(bytes, &idx); err != nil {
		return false, fmt.Errorf("parse bundle OCI index: %w", err)
	}
	if idx.ArtifactType == nextBundleType {
		return true, nil
	}
	for _, child := range idx.Manifests {
		if child.ArtifactType == nextBundleType {
			return true, nil
		}
	}
	return false, nil
}
