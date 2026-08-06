package introspect

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePackageRemote struct {
	root      packageRoot
	blobs     map[digest.Digest][]byte
	fetched   []string
	fetchedMu sync.Mutex
	fetchGate func(ocispec.Descriptor)
}

func (f *fakePackageRemote) Resolve(context.Context, ocispec.Platform) (packageRoot, error) {
	return f.root, nil
}

func (f *fakePackageRemote) Fetch(_ context.Context, descriptor ocispec.Descriptor) ([]byte, error) {
	if f.fetchGate != nil {
		f.fetchGate(descriptor)
	}
	f.fetchedMu.Lock()
	defer f.fetchedMu.Unlock()
	f.fetched = append(f.fetched, descriptor.Annotations[ocispec.AnnotationTitle])
	return f.blobs[descriptor.Digest], nil
}

type packageReaderFixture struct {
	remote              *fakePackageRemote
	zarfYAML            []byte
	component           []byte
	zarfDescriptor      ocispec.Descriptor
	componentDescriptor ocispec.Descriptor
}

func newPackageReaderFixture(t *testing.T) packageReaderFixture {
	t.Helper()
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
components:
  - name: apps
    charts:
      - name: alpha
        version: 1.0.0
`)
	component := tarArchive(t, map[string][]byte{"charts/alpha-1.0.0.tgz": []byte("chart")})
	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	componentDescriptor := packageDescriptor("components/apps.tar", component)
	return packageReaderFixture{
		remote: &fakePackageRemote{
			root: packageRoot{
				Descriptor: ocispec.Descriptor{Digest: digest.FromString("root")},
				Manifest: ocispec.Manifest{
					Config: ocispec.Descriptor{MediaType: zarfConfigMediaType},
					Layers: []ocispec.Descriptor{zarfDescriptor, componentDescriptor},
				},
			},
			blobs: map[digest.Digest][]byte{
				zarfDescriptor.Digest:      zarfYAML,
				componentDescriptor.Digest: component,
			},
		},
		zarfYAML:            zarfYAML,
		component:           component,
		zarfDescriptor:      zarfDescriptor,
		componentDescriptor: componentDescriptor,
	}
}

func TestPackageReaderSelectsEveryChartComponentWithoutImages(t *testing.T) {
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
components:
  - name: first
    charts:
      - name: alpha
        version: 1.0.0
  - name: second
    charts:
      - name: beta
        version: 2.0.0
  - name: image-only
    images:
      - example.test/image:latest
`)
	firstComponent := tarArchive(t, map[string][]byte{"charts/alpha-1.0.0.tgz": []byte("alpha chart")})
	secondComponent := tarArchive(t, map[string][]byte{"charts/beta-2.0.0.tgz": []byte("beta chart")})
	imageIndex := []byte("image index")

	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	firstDescriptor := packageDescriptor("components/first.tar", firstComponent)
	secondDescriptor := packageDescriptor("components/second.tar", secondComponent)
	imageDescriptor := packageDescriptor("images/index.json", imageIndex)
	rootDigest := digest.FromString("root manifest")
	remote := &fakePackageRemote{
		root: packageRoot{
			Descriptor: ocispec.Descriptor{Digest: rootDigest},
			Manifest: ocispec.Manifest{
				Config: ocispec.Descriptor{MediaType: zarfConfigMediaType},
				Layers: []ocispec.Descriptor{zarfDescriptor, firstDescriptor, secondDescriptor, imageDescriptor},
			},
		},
		blobs: map[digest.Digest][]byte{
			zarfDescriptor.Digest:   zarfYAML,
			firstDescriptor.Digest:  firstComponent,
			secondDescriptor.Digest: secondComponent,
			imageDescriptor.Digest:  imageIndex,
		},
	}

	reader := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "amd64"}, t.TempDir(), false)
	view, err := reader.Read(context.Background())
	require.NoError(t, err)

	assert.Equal(t, rootDigest, view.Snapshot.RootDigest)
	require.Len(t, view.Snapshot.Descriptors, 3)
	assert.Equal(t, []string{"zarf.yaml", "components/first.tar", "components/second.tar"}, remote.fetched)
	assert.Equal(t, zarfYAML, view.Files["zarf.yaml"])
	assert.Equal(t, firstComponent, view.Files["components/first.tar"])
	assert.Equal(t, secondComponent, view.Files["components/second.tar"])
	assert.NotContains(t, view.Files, "images/index.json")
}

func TestPackageReaderRejectsIncompleteOrUnverifiableSelectedDescriptors(t *testing.T) {
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
components:
  - name: apps
    charts:
      - name: alpha
        version: 1.0.0
`)
	component := tarArchive(t, map[string][]byte{"charts/alpha-1.0.0.tgz": []byte("chart")})
	corruptedComponent := append([]byte(nil), component...)
	corruptedComponent[0] ^= 0xff
	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	componentDescriptor := packageDescriptor("components/apps.tar", component)

	tests := []struct {
		name       string
		layers     []ocispec.Descriptor
		component  []byte
		diagnostic string
	}{
		{
			name:       "missing",
			layers:     []ocispec.Descriptor{zarfDescriptor},
			component:  component,
			diagnostic: `missing selected descriptor "components/apps.tar"`,
		},
		{
			name: "unsupported",
			layers: []ocispec.Descriptor{
				zarfDescriptor,
				func() ocispec.Descriptor {
					descriptor := componentDescriptor
					descriptor.MediaType = "application/octet-stream"
					return descriptor
				}(),
			},
			component:  component,
			diagnostic: `unsupported selected descriptor "components/apps.tar"`,
		},
		{
			name: "size-invalid",
			layers: []ocispec.Descriptor{
				zarfDescriptor,
				func() ocispec.Descriptor {
					descriptor := componentDescriptor
					descriptor.Size++
					return descriptor
				}(),
			},
			component:  component,
			diagnostic: `size-invalid selected descriptor "components/apps.tar"`,
		},
		{
			name:       "digest-invalid",
			layers:     []ocispec.Descriptor{zarfDescriptor, componentDescriptor},
			component:  corruptedComponent,
			diagnostic: `digest-invalid selected descriptor "components/apps.tar"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &fakePackageRemote{
				root: packageRoot{
					Descriptor: ocispec.Descriptor{Digest: digest.FromString("root")},
					Manifest: ocispec.Manifest{
						Config: ocispec.Descriptor{MediaType: zarfConfigMediaType},
						Layers: tt.layers,
					},
				},
				blobs: map[digest.Digest][]byte{
					zarfDescriptor.Digest:      zarfYAML,
					componentDescriptor.Digest: tt.component,
				},
			}
			reader := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "amd64"}, t.TempDir(), true)

			_, err := reader.Read(context.Background())
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.diagnostic)
		})
	}
}

func TestPackageReaderRejectsDuplicateControlPlaneFile(t *testing.T) {
	contents := []byte("kind: UDSBundle\n")
	descriptor := packageDescriptor(UDSBundleFile, contents)
	reader := newPackageReader(&fakePackageRemote{
		root: packageRoot{
			Descriptor: ocispec.Descriptor{Digest: digest.FromString("root")},
			Manifest: ocispec.Manifest{
				Config: ocispec.Descriptor{MediaType: UDSBundleMediaType},
				Layers: []ocispec.Descriptor{descriptor, descriptor},
			},
		},
		blobs: map[digest.Digest][]byte{descriptor.Digest: contents},
	}, ocispec.Platform{OS: "linux", Architecture: "amd64"}, t.TempDir(), true)

	_, err := reader.ReadFile(context.Background(), UDSBundleMediaType, UDSBundleFile)
	require.ErrorContains(t, err, `duplicate descriptor title "uds-bundle.yaml"`)
}

func TestPackageReaderRejectsIncompleteComponentContents(t *testing.T) {
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
components:
  - name: apps
    charts:
      - name: alpha
        version: 1.0.0
        valuesFiles:
          - managed.yaml
        templatedValuesFiles:
          - templated.yaml
`)
	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	tests := []struct {
		name       string
		component  []byte
		diagnostic string
	}{
		{
			name:       "missing declared chart",
			component:  tarArchive(t, nil),
			diagnostic: `declared chart "alpha"`,
		},
		{
			name: "missing managed values",
			component: tarArchive(t, map[string][]byte{
				"charts/alpha-1.0.0.tgz": []byte("chart"),
			}),
			diagnostic: `managed values file 0 for chart "alpha"`,
		},
		{
			name: "missing templated managed values",
			component: tarArchive(t, map[string][]byte{
				"charts/alpha-1.0.0.tgz": []byte("chart"),
				"values/alpha-1.0.0-0":   []byte("managed"),
			}),
			diagnostic: `managed values file 1 for chart "alpha"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			componentDescriptor := packageDescriptor("components/apps.tar", tt.component)
			remote := &fakePackageRemote{
				root: packageRoot{
					Descriptor: ocispec.Descriptor{Digest: digest.FromString("root")},
					Manifest: ocispec.Manifest{
						Config: ocispec.Descriptor{MediaType: zarfConfigMediaType},
						Layers: []ocispec.Descriptor{zarfDescriptor, componentDescriptor},
					},
				},
				blobs: map[digest.Digest][]byte{
					zarfDescriptor.Digest:      zarfYAML,
					componentDescriptor.Digest: tt.component,
				},
			}

			_, err := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "amd64"}, t.TempDir(), true).Read(context.Background())
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.diagnostic)
		})
	}
}

func TestPackageReaderCachesOnlyVerifiedDescriptorBytes(t *testing.T) {
	fixture := newPackageReaderFixture(t)
	remote := fixture.remote
	zarfYAML := fixture.zarfYAML
	zarfDescriptor := fixture.zarfDescriptor
	cacheDir := t.TempDir()
	platform := ocispec.Platform{OS: "multi", Architecture: "amd64"}

	_, err := newPackageReader(remote, platform, cacheDir, false).Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"zarf.yaml", "components/apps.tar"}, remote.fetched)

	remote.fetched = nil
	_, err = newPackageReader(remote, platform, cacheDir, false).Read(context.Background())
	require.NoError(t, err)
	assert.Empty(t, remote.fetched, "a verified cache hit must issue no blob fetch")

	_, err = newPackageReader(remote, platform, cacheDir, true).Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"zarf.yaml", "components/apps.tar"}, remote.fetched, "--no-cache must refresh the Package Snapshot bytes")

	remote.fetched = nil
	corruptedZarfYAML := append([]byte(nil), zarfYAML...)
	corruptedZarfYAML[0] ^= 0xff
	remote.blobs[zarfDescriptor.Digest] = corruptedZarfYAML
	_, err = newPackageReader(remote, platform, cacheDir, true).Read(context.Background())
	require.ErrorContains(t, err, "digest-invalid selected descriptor")
	assert.Equal(t, []string{"zarf.yaml"}, remote.fetched)

	cachePath := filepath.Join(cacheDir, string(zarfDescriptor.Digest.Algorithm()), zarfDescriptor.Digest.Encoded())
	cachedZarfYAML, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, zarfYAML, cachedZarfYAML, "--no-cache must not delete shared cached bytes before a verified refresh")
}

func TestPackageReaderDoesNotPublishUnverifiedDescriptorBytes(t *testing.T) {
	fixture := newPackageReaderFixture(t)
	remote := fixture.remote
	component := fixture.component
	componentDescriptor := fixture.componentDescriptor
	corruptedComponent := append([]byte(nil), component...)
	corruptedComponent[0] ^= 0xff
	remote.blobs[componentDescriptor.Digest] = corruptedComponent
	cacheDir := t.TempDir()

	_, err := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "amd64"}, cacheDir, false).Read(context.Background())
	require.ErrorContains(t, err, "digest-invalid selected descriptor")

	componentCachePath := filepath.Join(cacheDir, string(componentDescriptor.Digest.Algorithm()), componentDescriptor.Digest.Encoded())
	_, err = os.Stat(componentCachePath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPackageReaderPublishesVerifiedDescriptorBytesAtomically(t *testing.T) {
	fixture := newPackageReaderFixture(t)
	remote := fixture.remote
	zarfDescriptor := fixture.zarfDescriptor
	zarfFetches := sync.WaitGroup{}
	zarfFetches.Add(2)
	releaseFetches := make(chan struct{})
	remote.fetchGate = func(descriptor ocispec.Descriptor) {
		if descriptor.Digest == zarfDescriptor.Digest {
			zarfFetches.Done()
			<-releaseFetches
		}
	}
	cacheDir := t.TempDir()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "amd64"}, cacheDir, false).Read(context.Background())
			errs <- err
		}()
	}
	zarfFetches.Wait()
	close(releaseFetches)
	for range 2 {
		require.NoError(t, <-errs)
	}

	for descriptor, expected := range remote.blobs {
		cachePath := filepath.Join(cacheDir, string(descriptor.Algorithm()), descriptor.Encoded())
		actual, err := os.ReadFile(cachePath)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
		require.NoError(t, verifyDescriptorBytes(ocispec.Descriptor{Digest: descriptor, Size: int64(len(expected))}, actual))
	}
}

func TestPackageReaderReportsAvailablePlatformsWithoutFallback(t *testing.T) {
	child := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("amd64 manifest"),
		Size:      123,
		Platform: &ocispec.Platform{
			OS:           "multi",
			Architecture: "amd64",
			Variant:      "v8",
		},
	}
	index := ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{child},
	}
	indexBytes, err := json.Marshal(index)
	require.NoError(t, err)
	indexDigest := digest.FromBytes(indexBytes)

	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", indexDigest.String())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexBytes)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(registry.Close)

	originalTransport := http.DefaultTransport
	http.DefaultTransport = registry.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	source := "oci://" + strings.TrimPrefix(registry.URL, "https://") + "/example/package:latest"
	remote := newRegistryPackageRemote(source)
	reader := newPackageReader(remote, ocispec.Platform{OS: "multi", Architecture: "arm64"}, t.TempDir(), false)

	_, err = reader.Read(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "requested platform multi/arm64")
	assert.ErrorContains(t, err, "available platforms: multi/amd64/v8")

	exactReader := newPackageReader(newRegistryPackageRemote(source), ocispec.Platform{OS: "multi", Architecture: "amd64"}, t.TempDir(), false)
	_, err = exactReader.Read(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "requested platform multi/amd64")
	assert.ErrorContains(t, err, "available platforms: multi/amd64/v8")

	isZarf, err := IsZarfPackage(context.Background(), source, "arm64")
	require.Error(t, err)
	assert.False(t, isZarf)
	assert.ErrorContains(t, err, "requested platform multi/arm64")
	assert.ErrorContains(t, err, "available platforms: multi/amd64/v8")
}

func TestInspectRemoteZarfPackagePreservesAllChartsWithoutFetchingImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	alphaChart := chartArchive(t, "alpha", []byte("replicaCount: 1\nimageTag: default\n"))
	betaChart := chartArchive(t, "beta", []byte("enabled: true\n"))
	managedValues := []byte("replicaCount: 3\n")
	templatedValues := []byte("imageTag: '###ZARF_VAR_IMAGE_TAG###'\n")
	component := tarArchive(t, map[string][]byte{
		"charts/alpha-1.0.0.tgz": alphaChart,
		"charts/beta-1.0.0.tgz":  betaChart,
		"values/alpha-1.0.0-0":   managedValues,
		"values/alpha-1.0.0-1":   templatedValues,
	})
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: example
  version: 1.0.0
variables:
  - name: IMAGE_TAG
    default: stable
components:
  - name: apps
    charts:
      - name: alpha
        version: 1.0.0
        valuesFiles:
          - alpha-values.yaml
        templatedValuesFiles:
          - alpha-templated-values.yaml
      - name: beta
        version: 1.0.0
`)
	imageBlob := bytes.Repeat([]byte("image"), 1024)
	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	componentDescriptor := packageDescriptor("components/apps.tar", component)
	imageDescriptor := packageDescriptor("images/blobs/sha256/image", imageBlob)
	configBytes := []byte(`{}`)
	configDescriptor := ocispec.Descriptor{
		MediaType: zarfConfigMediaType,
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDescriptor,
		Layers:    []ocispec.Descriptor{zarfDescriptor, componentDescriptor, imageDescriptor},
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDigest := digest.FromBytes(manifestBytes)
	blobs := map[string][]byte{
		configDescriptor.Digest.String():    configBytes,
		zarfDescriptor.Digest.String():      zarfYAML,
		componentDescriptor.Digest.String(): component,
		imageDescriptor.Digest.String():     imageBlob,
	}
	titles := map[string]string{
		zarfDescriptor.Digest.String():      "zarf.yaml",
		componentDescriptor.Digest.String(): "components/apps.tar",
		imageDescriptor.Digest.String():     "images/blobs/sha256/image",
	}
	var requestedMu sync.Mutex
	var requested []string
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(manifestBytes)
		case strings.Contains(r.URL.Path, "/blobs/"):
			digestString := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if !strings.Contains(digestString, ":") {
				digestString = "sha256:" + digestString
			}
			contents, ok := blobs[digestString]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if title := titles[digestString]; title != "" {
				requestedMu.Lock()
				requested = append(requested, title)
				requestedMu.Unlock()
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(contents)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(registry.Close)

	originalTransport := http.DefaultTransport
	http.DefaultTransport = registry.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	source := "oci://" + strings.TrimPrefix(registry.URL, "https://") + "/example/package:latest"
	report, err := InspectZarfPackage(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
	require.NoError(t, err)
	require.Equal(t, []string{"apps/alpha", "apps/beta"}, report.ComponentOrder)
	alpha := report.ChartResults["apps/alpha"]
	require.NotNil(t, alpha)
	assert.NotEmpty(t, alpha.ValuesFiles)
	var replicaDefault any
	var imageTagDefault any
	for _, value := range alpha.Values {
		switch value.Path {
		case "replicaCount":
			replicaDefault = value.DefaultValue
		case "imageTag":
			imageTagDefault = value.DefaultValue
		}
	}
	assert.Equal(t, uint64(3), replicaDefault)
	assert.Equal(t, "###ZARF_VAR_IMAGE_TAG###", imageTagDefault)
	require.Len(t, report.Variables, 1)
	assert.Equal(t, "IMAGE_TAG", report.Variables[0].Name)
	assert.Equal(t, "stable", report.Variables[0].Default)
	require.DirExists(t, alpha.Source, "source files must remain available after remote inspection")
	require.FileExists(t, filepath.Join(alpha.Source, "values.yaml"))
	require.FileExists(t, filepath.Join(alpha.Source, "templates", "deployment.yaml"))

	requestedMu.Lock()
	defer requestedMu.Unlock()
	assert.Contains(t, requested, "zarf.yaml")
	assert.Contains(t, requested, "components/apps.tar")
	assert.NotContains(t, requested, "images/blobs/sha256/image")
}

func TestInspectRemoteUDSBundleUsesPackageViewsWithoutFetchingImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	alphaChart := chartArchive(t, "alpha", []byte("replicaCount: 1\n"))
	component := tarArchive(t, map[string][]byte{
		"charts/alpha-1.0.0.tgz": alphaChart,
		"values/alpha-1.0.0-0":   []byte("replicaCount: 3\n"),
	})
	zarfYAML := []byte(`kind: ZarfPackageConfig
metadata:
  name: apps
  version: 1.0.0
variables:
  - name: IMAGE_TAG
    default: stable
components:
  - name: apps
    charts:
      - name: alpha
        valuesFiles:
          - alpha-values.yaml
        version: 1.0.0
`)
	imageBlob := bytes.Repeat([]byte("image"), 1024)
	zarfDescriptor := packageDescriptor("zarf.yaml", zarfYAML)
	componentDescriptor := packageDescriptor("components/apps.tar", component)
	imageDescriptor := packageDescriptor("images/blobs/sha256/image", imageBlob)
	packageConfig := []byte(`{}`)
	packageConfigDescriptor := ocispec.Descriptor{
		MediaType: zarfConfigMediaType,
		Digest:    digest.FromBytes(packageConfig),
		Size:      int64(len(packageConfig)),
	}
	packageManifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    packageConfigDescriptor,
		Layers:    []ocispec.Descriptor{zarfDescriptor, componentDescriptor, imageDescriptor},
	}
	packageManifestBytes, err := json.Marshal(packageManifest)
	require.NoError(t, err)
	packageManifestDescriptor := packageDescriptor("packages/apps", packageManifestBytes)
	bundleYAML := []byte(`kind: UDSBundle
metadata:
  name: example
  version: 1.0.0
packages:
  - name: apps
    ref: v1@` + packageManifestDescriptor.Digest.String() + `
    overrides:
      apps:
        alpha:
          values:
            - path: replicaCount
              value: 5
`)
	bundleDescriptor := packageDescriptor(UDSBundleFile, bundleYAML)
	bundleConfig := []byte(`{}`)
	bundleConfigDescriptor := ocispec.Descriptor{
		MediaType: UDSBundleMediaType,
		Digest:    digest.FromBytes(bundleConfig),
		Size:      int64(len(bundleConfig)),
	}
	bundleManifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    bundleConfigDescriptor,
		Layers:    []ocispec.Descriptor{bundleDescriptor, packageManifestDescriptor},
	}
	bundleManifestBytes, err := json.Marshal(bundleManifest)
	require.NoError(t, err)
	activeManifestBytes := bundleManifestBytes
	activeManifestDigest := digest.FromBytes(activeManifestBytes)
	blobs := map[string][]byte{
		bundleDescriptor.Digest.String():          bundleYAML,
		packageManifestDescriptor.Digest.String(): packageManifestBytes,
		zarfDescriptor.Digest.String():            zarfYAML,
		componentDescriptor.Digest.String():       component,
		imageDescriptor.Digest.String():           imageBlob,
	}
	titles := map[string]string{
		bundleDescriptor.Digest.String():          UDSBundleFile,
		packageManifestDescriptor.Digest.String(): "packages/apps",
		zarfDescriptor.Digest.String():            "zarf.yaml",
		componentDescriptor.Digest.String():       "components/apps.tar",
		imageDescriptor.Digest.String():           "images/blobs/sha256/image",
	}
	var requestedMu sync.Mutex
	var manifestMu sync.Mutex
	var requested []string
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			manifestMu.Lock()
			manifestBytes := activeManifestBytes
			manifestDigest := activeManifestDigest
			manifestMu.Unlock()
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(manifestBytes)
		case strings.Contains(r.URL.Path, "/blobs/"):
			digestString := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if !strings.Contains(digestString, ":") {
				digestString = "sha256:" + digestString
			}
			contents, ok := blobs[digestString]
			if !ok {
				http.NotFound(w, r)
				return
			}
			requestedMu.Lock()
			requested = append(requested, titles[digestString])
			requestedMu.Unlock()
			w.Header().Set("Content-Length", strconv.Itoa(len(contents)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(contents)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(registry.Close)

	originalTransport := http.DefaultTransport
	http.DefaultTransport = registry.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	source := "oci://" + strings.TrimPrefix(registry.URL, "https://") + "/example/bundle:latest"
	report, err := InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
	require.NoError(t, err)
	require.Len(t, report.Variables, 1)
	assert.Equal(t, "apps.IMAGE_TAG", report.Variables[0].Name)
	assert.Equal(t, uint64(3), report.UDSOverrideValues[0].OriginalDefault)
	require.Equal(t, []string{"apps/apps/alpha"}, report.ComponentOrder)
	require.NotNil(t, report.ChartResults["apps/apps/alpha"])
	alpha := report.ChartResults["apps/apps/alpha"]
	require.DirExists(t, alpha.Source, "source files must remain available after remote inspection")
	require.FileExists(t, filepath.Join(alpha.Source, "values.yaml"))
	require.FileExists(t, filepath.Join(alpha.Source, "templates", "deployment.yaml"))
	require.Len(t, report.UDSOverrideValues, 1)
	assert.Equal(t, uint64(5), report.UDSOverrideValues[0].Value)

	requestedMu.Lock()
	assert.Contains(t, requested, UDSBundleFile)
	assert.Contains(t, requested, "packages/apps")
	assert.Contains(t, requested, "zarf.yaml")
	assert.Contains(t, requested, "components/apps.tar")
	assert.NotContains(t, requested, "images/blobs/sha256/image")
	requestCount := len(requested)
	requestedMu.Unlock()

	_, err = InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{})
	require.NoError(t, err)
	requestedMu.Lock()
	assert.Len(t, requested, requestCount)
	requestedMu.Unlock()
	missingPackageBundle := []byte(`kind: UDSBundle
metadata:
  name: incomplete
packages:
  - name: missing
    ref: v1@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`)
	missingPackageDescriptor := packageDescriptor(UDSBundleFile, missingPackageBundle)
	blobs[missingPackageDescriptor.Digest.String()] = missingPackageBundle
	titles[missingPackageDescriptor.Digest.String()] = UDSBundleFile
	incompleteManifest := bundleManifest
	incompleteManifest.Layers = []ocispec.Descriptor{missingPackageDescriptor}
	activeManifestBytes, err = json.Marshal(incompleteManifest)
	require.NoError(t, err)
	activeManifestDigest = digest.FromBytes(activeManifestBytes)

	_, err = InspectUDSBundle(context.Background(), source, "", "amd64", IntrospectOptions{NoCache: true})
	require.ErrorContains(t, err, `failed to form Package View for UDS package "missing"`)
}

func chartArchive(t *testing.T, name string, values []byte) []byte {
	t.Helper()
	files := map[string][]byte{
		name + "/Chart.yaml":                []byte("apiVersion: v2\nname: " + name + "\nversion: 1.0.0\n"),
		name + "/values.yaml":               values,
		name + "/templates/deployment.yaml": []byte("replicas: {{ .Values.replicaCount | default 1 }}\nimageTag: {{ .Values.imageTag | default \"latest\" }}\nenabled: {{ .Values.enabled | default true }}\n"),
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	writeTar(t, writer, files)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func tarArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writeTar(t, &buffer, files)
	return buffer.Bytes()
}

func writeTar(t *testing.T, writer io.Writer, files map[string][]byte) {
	t.Helper()
	tarWriter := tar.NewWriter(writer)
	for path, contents := range files {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(contents))}))
		_, err := tarWriter.Write(contents)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
}

func packageDescriptor(title string, contents []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: zarfLayerMediaType,
		Digest:    digest.FromBytes(contents),
		Size:      int64(len(contents)),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: title,
		},
	}
}
