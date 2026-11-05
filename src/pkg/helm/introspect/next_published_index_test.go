package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// The public k3d-core-slim-dev-next tag wraps the canonical bundle index in
// another index whose platform OS is "multi". It must not reach Helm's tag parser.
func TestPublishedNextIndexRoutesAndInspectsWithoutConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	child, blobs, imageDigest := nextFixture(t)
	childDigest := digest.FromBytes(child)
	blobs[childDigest.String()] = child
	unusedArch := digest.FromString("unselected architecture must not be fetched")
	root := ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{MediaType: ocispec.MediaTypeImageIndex, ArtifactType: nextBundleType, Digest: unusedArch, Size: int64(len(child)), Platform: &ocispec.Platform{OS: "multi", Architecture: "arm64"}},
			{MediaType: ocispec.MediaTypeImageIndex, ArtifactType: nextBundleType, Digest: childDigest, Size: int64(len(child)), Platform: &ocispec.Platform{OS: "multi", Architecture: "amd64"}},
		}}
	rootBytes, err := json.Marshal(root)
	require.NoError(t, err)
	rootDigest := digest.FromBytes(rootBytes)
	blobs[rootDigest.String()] = rootBytes
	var mu sync.Mutex
	requested := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if key == "latest" {
			key = rootDigest.String()
		}
		mu.Lock()
		requested[key]++
		mu.Unlock()
		data, exists := blobs[key]
		if !exists {
			http.NotFound(w, r)
			return
		}
		media := ocispec.MediaTypeImageManifest
		if key == rootDigest.String() || key == childDigest.String() {
			media = ocispec.MediaTypeImageIndex
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Docker-Content-Digest", key)
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	source := "oci://" + strings.TrimPrefix(server.URL, "https://") + "/example/published-next:latest"
	isZarf, err := IsZarfPackage(context.Background(), source, "amd64")
	require.NoError(t, err)
	require.False(t, isZarf)
	isBundle, err := IsUDSBundle(context.Background(), source, "amd64")
	require.NoError(t, err)
	require.True(t, isBundle)
	got, err := InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
	require.NoError(t, err)
	nextAssertFixture(t, got)
	mu.Lock()
	defer mu.Unlock()
	require.Zero(t, requested[imageDigest.String()])
	require.Zero(t, requested[unusedArch.String()])
}
