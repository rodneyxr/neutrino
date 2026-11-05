package introspect

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func nextArchiveEntry(source, target string, limit int64) ([]byte, error) {
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("missing archive entry %q", target)
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(header.Name, "./")
		if name != target {
			continue
		}
		if !nextSafePath(name) || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("unsafe archive entry %q", name)
		}
		if header.Size < 0 || header.Size > limit {
			return nil, fmt.Errorf("oversized archive entry %q", name)
		}
		return io.ReadAll(io.LimitReader(reader, limit+1))
	}
}
func inspectNextArchive(ctx context.Context, source, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	index, err := nextArchiveEntry(source, "oci/index.json", nextMaxControlBytes)
	if err != nil {
		return nil, err
	}
	fetch := func(_ context.Context, d ocispec.Descriptor) ([]byte, error) {
		if err := d.Digest.Validate(); err != nil {
			return nil, err
		}
		if d.Size < 0 || d.Size > 256<<20 {
			return nil, fmt.Errorf("oversized OCI descriptor %s", d.Digest)
		}
		blob := path.Join("oci", "blobs", string(d.Digest.Algorithm()), d.Digest.Encoded())
		data, err := nextArchiveEntry(source, blob, d.Size)
		if err != nil {
			return nil, err
		}
		if err := verifyDescriptorBytes(d, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return inspectNextIndex(ctx, index, source, version, arch, opts, fetch, "")
}
