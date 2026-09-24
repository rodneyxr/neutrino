package introspect

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func nextFixture(t *testing.T) ([]byte, map[string][]byte, digest.Digest) {
	t.Helper()
	blobs := map[string][]byte{}
	blob := func(media, title string, data []byte) ocispec.Descriptor {
		d := ocispec.Descriptor{MediaType: media, Digest: digest.FromBytes(data), Size: int64(len(data))}
		if title != "" {
			d.Annotations = map[string]string{ocispec.AnnotationTitle: title}
		}
		blobs[d.Digest.String()] = data
		return d
	}
	zarf := blob(zarfLayerMediaType, "zarf.yaml", []byte("kind: ZarfPackageConfig\nmetadata:\n  name: test\n  version: 1.0.0\nvalues:\n  files: [defaults.yaml]\n"))
	defaults := blob(zarfLayerMediaType, "values.yaml", []byte("replicas: 1\n"))
	image := blob(zarfLayerMediaType, "images/unused", []byte("image bytes should never be fetched"))
	zarfConfig := blob(zarfConfigMediaType, "", []byte("{}"))
	pkg := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: zarfConfig, Layers: []ocispec.Descriptor{zarf, defaults, image}}
	pkgJSON, err := json.Marshal(pkg)
	require.NoError(t, err)
	packageDesc := blob(ocispec.MediaTypeImageManifest, "", pkgJSON)
	packageDesc.Annotations = map[string]string{nextPackageAnnotation: "app"}
	empty := blob("application/vnd.oci.empty.v1+json", "", []byte("{}"))
	empty.Data = []byte("{}")
	hcl := blob(nextHCLType, nextBundleFile, []byte("uds { bundle_api_version = \"uds.dev/v1alpha1\" }\nmetadata { name = \"test\" }\npackage \"app\" {\n source = \"unused\"\n signature_verification { verify = false }\n values_files = [\"values.yaml\"]\n}\n"))
	vars := blob(nextHCLType, nextDefaultsFile, []byte("variables = { replicas = 3 }\n"))
	values := blob(nextValuesType, "values/app/0.yaml", []byte("replicas: {{ .vars.replicas }}\n"))
	def := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: empty, Layers: []ocispec.Descriptor{hcl, vars, values}}
	defJSON, err := json.Marshal(def)
	require.NoError(t, err)
	defDesc := blob(ocispec.MediaTypeImageManifest, "", defJSON)
	defDesc.ArtifactType = nextDefinitionType
	idx := ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, ArtifactType: nextBundleType, Manifests: []ocispec.Descriptor{defDesc, packageDesc}, Annotations: map[string]string{nextArchAnnotation: "amd64"}}
	index, err := json.Marshal(idx)
	require.NoError(t, err)
	return index, blobs, image.Digest
}
func nextAssertFixture(t *testing.T, got *ZarfPackageIntrospection) {
	t.Helper()
	require.NotNil(t, got.Bundle)
	require.Equal(t, "next", got.Bundle.Format)
	require.Len(t, got.Bundle.Packages, 1)
	require.Equal(t, "app", got.Bundle.Packages[0].ID)
	require.Len(t, got.PackageValues, 1)
	require.Equal(t, "app", got.PackageValues[0].PackageName)
	require.EqualValues(t, 3, got.PackageValues[0].Values["replicas"])
	require.Len(t, got.PackageValues[0].Files, 2)
}
func TestNextSourceDirectoryRoutesMalformedDefinitionToParser(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, nextBundleFile), []byte("metadata { name = \n"), 0600))
	recognized, err := IsUDSBundle(context.Background(), dir, "amd64")
	require.NoError(t, err)
	require.True(t, recognized)
	_, err = InspectUDSBundle(context.Background(), dir, "", "amd64", IntrospectOptions{})
	require.ErrorContains(t, err, "parse bundle.uds.hcl")
}

func TestNextCreatedArchiveInspectsDeclaredPackageWithoutImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	index, blobs, unused := nextFixture(t)
	var archive bytes.Buffer
	zw, err := zstd.NewWriter(&archive)
	require.NoError(t, err)
	tw := tar.NewWriter(zw)
	add := func(name string, data []byte) {
		t.Helper()
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}
	add("oci/index.json", index)
	for key, data := range blobs {
		if key == unused.String() {
			continue
		}
		add("oci/blobs/sha256/"+strings.TrimPrefix(key, "sha256:"), data)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	file := filepath.Join(t.TempDir(), "next.tar.zst")
	require.NoError(t, os.WriteFile(file, archive.Bytes(), 0600))
	got, err := InspectUDSBundle(context.Background(), file, "", "amd64", IntrospectOptions{})
	require.NoError(t, err)
	nextAssertFixture(t, got)
}
func TestNextRemoteIndexDoesNotFetchImageBlob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	index, blobs, unused := nextFixture(t)
	idxDigest := digest.FromBytes(index)
	var mu sync.Mutex
	requested := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "/manifests/") {
			key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			data := index
			media := ocispec.MediaTypeImageIndex
			hash := idxDigest
			if key != "latest" {
				if !strings.HasPrefix(key, "sha256:") {
					key = "sha256:" + key
				}
				if key != idxDigest.String() {
					var ok bool
					data, ok = blobs[key]
					if !ok {
						http.NotFound(w, r)
						return
					}
					media = ocispec.MediaTypeImageManifest
					hash = digest.FromBytes(data)
				}
			}
			w.Header().Set("Content-Type", media)
			w.Header().Set("Docker-Content-Digest", hash.String())
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		if strings.Contains(r.URL.Path, "/blobs/") {
			key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if !strings.HasPrefix(key, "sha256:") {
				key = "sha256:" + key
			}
			mu.Lock()
			requested[key]++
			mu.Unlock()
			data, ok := blobs[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	source := "oci://" + strings.TrimPrefix(server.URL, "https://") + "/example/next:latest"
	got, err := InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
	require.NoError(t, err)
	nextAssertFixture(t, got)
	mu.Lock()
	defer mu.Unlock()
	require.Zero(t, requested[unused.String()], "embedded image blob must not be fetched")
}
