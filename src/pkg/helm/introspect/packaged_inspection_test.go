package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPackagedInspectionCompletenessAcrossDirectLegacyAndNext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var chartRequests atomic.Int32
	chartServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chartRequests.Add(1)
		http.NotFound(w, r)
	}))
	defer chartServer.Close()
	for _, layout := range []string{"root", "nested"} {
		for _, state := range []string{"complete", "required-missing", "values-missing", "invalid-chart"} {
			t.Run(layout+"/"+state, func(t *testing.T) {
				blobs := map[string][]byte{}
				blob := func(media, title string, data []byte) ocispec.Descriptor {
					d := ocispec.Descriptor{MediaType: media, Digest: digest.FromBytes(data), Size: int64(len(data))}
					if title != "" {
						d.Annotations = map[string]string{ocispec.AnnotationTitle: title}
					}
					blobs[d.Digest.String()] = data
					return d
				}
				manifest := func(config ocispec.Descriptor, layers ...ocispec.Descriptor) []byte {
					data, err := json.Marshal(ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, Config: config, Layers: layers})
					require.NoError(t, err)
					return data
				}
				zarf := []byte(fmt.Sprintf(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
components:
  - name: apps
    required: true
    charts:
      - name: alpha
        version: 1.0.0
        valuesFiles: [first.yaml, second.yaml]
        templatedValuesFiles: [runtime.yaml]
      - name: beta
        version: 1.0.0
  - name: optional
    required: false
    charts:
      - name: omitted
        version: 1.0.0
        url: %s
`, chartServer.URL))
				prefix := ""
				if layout == "nested" {
					prefix = "apps/"
				}
				files := map[string][]byte{
					prefix + "charts/alpha-1.0.0.tgz": chartArchive(t, "alpha", []byte("replicaCount: 1\n")),
					prefix + "charts/beta.tgz":        chartArchive(t, "beta", []byte("enabled: false\n")),
					prefix + "values/alpha-1.0.0-0":   []byte("replicaCount: 2\n"),
					prefix + "values/alpha-1.0.0-1":   []byte("replicaCount: 7\n"),
					prefix + "values/alpha-1.0.0-2":   []byte("replicaCount: '{{ .Values.runtime }}'\n"),
				}
				if state == "values-missing" {
					delete(files, prefix+"values/alpha-1.0.0-1")
				}
				if state == "invalid-chart" {
					files[prefix+"charts/beta.tgz"] = []byte("not a chart archive")
				}
				cfg := blob(zarfConfigMediaType, "", []byte("{}"))
				image := blob(zarfLayerMediaType, "images/forbidden", []byte("never download"))
				layers := []ocispec.Descriptor{blob(zarfLayerMediaType, "zarf.yaml", zarf), image}
				if state != "required-missing" {
					layers = append(layers, blob(zarfLayerMediaType, "components/apps.tar", tarArchive(t, files)))
				}
				pkg := manifest(cfg, layers...)
				pkgDesc := blob(ocispec.MediaTypeImageManifest, "", pkg)
				legacyPkg := pkgDesc
				legacyPkg.MediaType = zarfLayerMediaType
				legacyPkg.Annotations = map[string]string{ocispec.AnnotationTitle: "packages/app"}
				legacy := manifest(blob(UDSBundleMediaType, "", []byte("{}")), blob(zarfLayerMediaType, UDSBundleFile, []byte("kind: UDSBundle\nmetadata:\n  name: test\npackages:\n  - name: app\n    ref: v1@"+pkgDesc.Digest.String()+"\n")), legacyPkg)
				empty := blob("application/vnd.oci.empty.v1+json", "", []byte("{}"))
				empty.Data = []byte("{}")
				definition := manifest(empty, blob(nextHCLType, nextBundleFile, []byte("uds { bundle_api_version = \"uds.dev/v1alpha1\" }\nmetadata { name = \"test\" }\npackage \"app\" {\n source = \"unused\"\n signature_verification { verify = false }\n}\n")))
				defDesc := blob(ocispec.MediaTypeImageManifest, "", definition)
				defDesc.ArtifactType = nextDefinitionType
				pkgDesc.Annotations = map[string]string{nextPackageAnnotation: "app"}
				next, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, ArtifactType: nextBundleType, Manifests: []ocispec.Descriptor{defDesc, pkgDesc}, Annotations: map[string]string{nextArchAnnotation: "amd64"}})
				require.NoError(t, err)
				blobs[digest.FromBytes(legacy).String()] = legacy
				blobs[digest.FromBytes(next).String()] = next
				var imageRequests atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v2/" {
						w.WriteHeader(http.StatusOK)
						return
					}
					key := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					var data []byte
					media := ocispec.MediaTypeImageManifest
					if strings.Contains(r.URL.Path, "/manifests/") {
						switch key {
						case "direct":
							data = pkg
						case "legacy":
							data = legacy
						case "next", digest.FromBytes(next).String():
							data = next
							media = ocispec.MediaTypeImageIndex
						default:
							data = blobs[key]
						}
						w.Header().Set("Docker-Content-Digest", digest.FromBytes(data).String())
						w.Header().Set("Content-Type", media)
					} else {
						if key == image.Digest.String() {
							imageRequests.Add(1)
						}
						data = blobs[key]
					}
					if data == nil {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprint(len(data)))
					_, _ = w.Write(data)
				}))
				defer server.Close()
				old := http.DefaultTransport
				http.DefaultTransport = server.Client().Transport
				defer func() { http.DefaultTransport = old }()
				for _, format := range []string{"direct", "legacy", "next"} {
					source := "oci://" + strings.TrimPrefix(server.URL, "https://") + "/example/package:" + format
					var got *ZarfPackageIntrospection
					if format == "direct" {
						got, err = InspectZarfPackage(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
					} else {
						got, err = InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
					}
					if state != "complete" {
						require.Error(t, err, format)
						require.Nil(t, got, format)
						switch state {
						case "required-missing":
							require.ErrorContains(t, err, "components/apps.tar")
						case "values-missing":
							require.ErrorContains(t, err, `managed values file 1 for chart "alpha"`)
						case "invalid-chart":
							require.ErrorContains(t, err, "beta")
						}
						continue
					}
					require.NoError(t, err, format)
					keyPrefix := ""
					if format != "direct" {
						keyPrefix = "app/"
					}
					require.Equal(t, []string{keyPrefix + "apps/alpha", keyPrefix + "apps/beta"}, got.ComponentOrder)
					alpha := got.ChartResults[keyPrefix+"apps/alpha"]
					var replicas interface{}
					for _, value := range alpha.Values {
						if value.Path == "replicaCount" {
							replicas = value.DefaultValue
						}
					}
					require.EqualValues(t, 7, replicas, "managed values retain declaration order")
					require.Len(t, got.PackageValues, 1)
					require.True(t, got.PackageValues[0].Usages[0].Runtime)
				}
				require.Zero(t, imageRequests.Load())
			})
		}
	}
	require.Zero(t, chartRequests.Load(), "omitted chart URLs are not source fallbacks")
}
