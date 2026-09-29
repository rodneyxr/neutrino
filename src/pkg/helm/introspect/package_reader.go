package introspect

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
	"github.com/zarf-dev/zarf/src/pkg/packager/layout"
)

const (
	zarfConfigMediaType = "application/vnd.zarf.config.v1+json"
	zarfLayerMediaType  = "application/vnd.zarf.layer.v1.blob"
)

type packageRemote interface {
	Resolve(context.Context, ocispec.Platform) (packageRoot, error)
	Fetch(context.Context, ocispec.Descriptor) ([]byte, error)
}

type packageRoot struct {
	Descriptor ocispec.Descriptor
	Manifest   ocispec.Manifest
}

type packageSnapshot struct {
	RootDigest  digest.Digest
	Descriptors []ocispec.Descriptor
}

type packageView struct {
	Snapshot packageSnapshot
	Files    map[string][]byte
}

type resolvedPackageRemote struct {
	root  packageRoot
	fetch func(context.Context, ocispec.Descriptor) ([]byte, error)
}

func (r resolvedPackageRemote) Resolve(context.Context, ocispec.Platform) (packageRoot, error) {
	return r.root, nil
}

func (r resolvedPackageRemote) Fetch(ctx context.Context, descriptor ocispec.Descriptor) ([]byte, error) {
	return r.fetch(ctx, descriptor)
}

func (v *packageView) materialize(root string) error {
	for title, contents := range v.Files {
		cleanTitle := path.Clean(title)
		if cleanTitle != title || path.IsAbs(title) || strings.HasPrefix(cleanTitle, "../") {
			return fmt.Errorf("unsupported package layout: unsafe selected descriptor title %q", title)
		}
		target := filepath.Join(root, filepath.FromSlash(title))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("failed to create package view directory for %q: %w", title, err)
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			return fmt.Errorf("failed to materialize selected descriptor %q: %w", title, err)
		}
	}
	return nil
}

type packageReader struct {
	remote   packageRemote
	platform ocispec.Platform
	cacheDir string
	noCache  bool
}

func newPackageReader(remote packageRemote, platform ocispec.Platform, cacheDir string, noCache bool) *packageReader {
	return &packageReader{
		remote:   remote,
		platform: platform,
		cacheDir: cacheDir,
		noCache:  noCache,
	}
}

func (r *packageReader) ReadFile(ctx context.Context, configMediaType string, title string) (*packageView, error) {
	root, err := r.remote.Resolve(ctx, r.platform)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve package root: %w", err)
	}
	// Legacy UDS bundle roots also appear with Zarf's blob media type as config.
	// Keep this exception scoped to bundle control planes, not embedded packages.
	if root.Manifest.Config.MediaType != configMediaType &&
		!(configMediaType == UDSBundleMediaType && root.Manifest.Config.MediaType == zarfLayerMediaType) {
		return nil, fmt.Errorf("unsupported package root config media type %q", root.Manifest.Config.MediaType)
	}
	var selected ocispec.Descriptor
	for _, descriptor := range root.Manifest.Layers {
		if descriptor.Annotations[ocispec.AnnotationTitle] != title {
			continue
		}
		if selected.Digest != "" {
			return nil, fmt.Errorf("unsupported package layout: duplicate descriptor title %q", title)
		}
		selected = descriptor
	}
	if selected.Digest == "" {
		return nil, fmt.Errorf("incomplete package layout: missing selected descriptor %q", title)
	}
	contents, err := r.fetchVerified(ctx, selected)
	if err != nil {
		return nil, err
	}
	return &packageView{
		Snapshot: packageSnapshot{RootDigest: root.Descriptor.Digest, Descriptors: []ocispec.Descriptor{selected}},
		Files:    map[string][]byte{title: contents},
	}, nil
}

func (r *packageReader) ReadEmbedded(ctx context.Context, descriptor ocispec.Descriptor, packageFetch func(context.Context, ocispec.Descriptor) ([]byte, error)) (*packageView, error) {
	if descriptor.MediaType != ocispec.MediaTypeImageManifest && descriptor.MediaType != zarfLayerMediaType {
		return nil, fmt.Errorf("unsupported selected embedded package manifest media type %q", descriptor.MediaType)
	}
	contents, err := r.fetchVerifiedMedia(ctx, descriptor, descriptor.MediaType)
	if err != nil {
		return nil, err
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse selected embedded package manifest %q: %w", descriptor.Digest, err)
	}
	embedded := newPackageReader(resolvedPackageRemote{
		root:  packageRoot{Descriptor: descriptor, Manifest: manifest},
		fetch: packageFetch,
	}, r.platform, r.cacheDir, r.noCache)
	return embedded.Read(ctx)
}

func (r *packageReader) Read(ctx context.Context) (*packageView, error) {
	root, err := r.remote.Resolve(ctx, r.platform)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve package root: %w", err)
	}
	if root.Manifest.Config.MediaType != zarfConfigMediaType {
		return nil, fmt.Errorf("unsupported package root config media type %q", root.Manifest.Config.MediaType)
	}

	byTitle := make(map[string]ocispec.Descriptor, len(root.Manifest.Layers))
	for _, descriptor := range root.Manifest.Layers {
		title := descriptor.Annotations[ocispec.AnnotationTitle]
		if title == "" {
			continue
		}
		if _, exists := byTitle[title]; exists {
			return nil, fmt.Errorf("unsupported package layout: duplicate descriptor title %q", title)
		}
		byTitle[title] = descriptor
	}

	zarfDescriptor, ok := byTitle["zarf.yaml"]
	if !ok {
		return nil, fmt.Errorf("incomplete package layout: missing selected descriptor %q", "zarf.yaml")
	}
	zarfBytes, err := r.fetchVerified(ctx, zarfDescriptor)
	if err != nil {
		return nil, err
	}

	var pkg v1alpha1.ZarfPackage
	if err := yaml.Unmarshal(zarfBytes, &pkg); err != nil {
		return nil, fmt.Errorf("failed to parse selected descriptor %q: %w", "zarf.yaml", err)
	}

	selected := []ocispec.Descriptor{zarfDescriptor}
	files := map[string][]byte{"zarf.yaml": zarfBytes}
	// Assembled Zarf v0.82.0 packages store merged package defaults and schema
	// at fixed root titles, not at the source paths retained in zarf.yaml.
	for _, title := range []string{layout.ValuesYAML, layout.ValuesSchema} {
		required := (title == layout.ValuesYAML && len(pkg.Values.Files) > 0) ||
			(title == layout.ValuesSchema && pkg.Values.Schema != "")
		descriptor, exists := byTitle[title]
		if !exists {
			if required {
				return nil, fmt.Errorf("incomplete package layout: missing selected descriptor %q", title)
			}
			continue
		}
		contents, err := r.fetchVerified(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		selected = append(selected, descriptor)
		files[title] = contents
	}
	for _, component := range pkg.Components {
		if len(component.Charts) == 0 {
			continue
		}
		if component.Name == "" || path.Base(component.Name) != component.Name || component.Name == "." {
			return nil, fmt.Errorf("unsupported package layout: invalid selected component name %q", component.Name)
		}
		title := path.Join("components", component.Name+".tar")
		if strings.HasPrefix(title, "images/") || title == "images" {
			return nil, fmt.Errorf("unsupported package layout: selected component %q resolves under images", component.Name)
		}
		descriptor, ok := byTitle[title]
		if !ok {
			if component.Required != nil && !*component.Required {
				continue // Explicitly optional component was not included in this package artifact.
			}
			return nil, fmt.Errorf("incomplete package layout: missing selected descriptor %q for component %q", title, component.Name)
		}
		contents, err := r.fetchVerified(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		if err := validateSelectedComponent(component, descriptor, contents); err != nil {
			return nil, err
		}
		selected = append(selected, descriptor)
		files[title] = contents
	}

	return &packageView{
		Snapshot: packageSnapshot{
			RootDigest:  root.Descriptor.Digest,
			Descriptors: selected,
		},
		Files: files,
	}, nil
}

func validateSelectedComponent(component v1alpha1.ZarfComponent, descriptor ocispec.Descriptor, contents []byte) error {
	entries := make(map[string]byte)
	reader := tar.NewReader(bytes.NewReader(contents))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("unsupported selected descriptor %q component archive: %w", descriptor.Annotations[ocispec.AnnotationTitle], err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		cleanName := path.Clean(name)
		if cleanName == "." {
			continue
		}
		if cleanName != name || path.IsAbs(name) || strings.HasPrefix(cleanName, "../") {
			return fmt.Errorf("unsupported selected descriptor %q component archive entry %q", descriptor.Annotations[ocispec.AnnotationTitle], header.Name)
		}
		entries[cleanName] = header.Typeflag
	}

	chartsBase := "charts"
	valuesBase := "values"
	if !hasArchivePrefix(entries, chartsBase+"/") {
		chartsBase = path.Join(component.Name, chartsBase)
		valuesBase = path.Join(component.Name, valuesBase)
	}
	for _, chart := range component.Charts {
		chartArchive := path.Join(chartsBase, fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version))
		alternateArchive := path.Join(chartsBase, chart.Name+".tgz")
		chartYAML := path.Join(chartsBase, chart.Name, "Chart.yaml")
		if !isRegularArchiveEntry(entries, chartArchive) &&
			!isRegularArchiveEntry(entries, alternateArchive) &&
			!isRegularArchiveEntry(entries, chartYAML) {
			return fmt.Errorf("incomplete selected descriptor %q: declared chart %q was not found", descriptor.Annotations[ocispec.AnnotationTitle], chart.Name)
		}
		managedValuesCount := len(chart.ValuesFiles) + len(chart.TemplatedValuesFiles)
		for i := range managedValuesCount {
			valuesPath := path.Join(valuesBase, fmt.Sprintf("%s-%s-%d", chart.Name, chart.Version, i))
			if !isRegularArchiveEntry(entries, valuesPath) {
				return fmt.Errorf("incomplete selected descriptor %q: managed values file %d for chart %q was not found", descriptor.Annotations[ocispec.AnnotationTitle], i, chart.Name)
			}
		}
	}
	return nil
}

func hasArchivePrefix(entries map[string]byte, prefix string) bool {
	for name := range entries {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isRegularArchiveEntry(entries map[string]byte, name string) bool {
	typeflag, exists := entries[name]
	return exists && (typeflag == tar.TypeReg || typeflag == tar.TypeRegA)
}

func (r *packageReader) fetchVerified(ctx context.Context, descriptor ocispec.Descriptor) ([]byte, error) {
	return r.fetchVerifiedMedia(ctx, descriptor, zarfLayerMediaType)
}

func (r *packageReader) fetchVerifiedMedia(ctx context.Context, descriptor ocispec.Descriptor, mediaType string) ([]byte, error) {
	title := descriptor.Annotations[ocispec.AnnotationTitle]
	if descriptor.MediaType != mediaType {
		return nil, fmt.Errorf("unsupported selected descriptor %q media type %q", title, descriptor.MediaType)
	}
	if descriptor.Size <= 0 {
		return nil, fmt.Errorf("size-invalid selected descriptor %q: expected a positive size, got %d", title, descriptor.Size)
	}
	if err := descriptor.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("digest-invalid selected descriptor %q: %w", title, err)
	}

	cachePath := filepath.Join(r.cacheDir, string(descriptor.Digest.Algorithm()), descriptor.Digest.Encoded())
	if !r.noCache && r.cacheDir != "" {
		contents, err := os.ReadFile(cachePath)
		if err == nil {
			if verifyErr := verifyDescriptorBytes(descriptor, contents); verifyErr == nil {
				return contents, nil
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read cached descriptor %q: %w", title, err)
		}
	}

	contents, err := r.remote.Fetch(ctx, descriptor)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch selected descriptor %q: %w", title, err)
	}
	if err := verifyDescriptorBytes(descriptor, contents); err != nil {
		return nil, err
	}
	if r.cacheDir != "" {
		if err := publishDescriptorBytes(cachePath, contents); err != nil {
			return nil, fmt.Errorf("failed to cache verified selected descriptor %q: %w", title, err)
		}
	}
	return contents, nil
}

func verifyDescriptorBytes(descriptor ocispec.Descriptor, contents []byte) error {
	title := descriptor.Annotations[ocispec.AnnotationTitle]
	if int64(len(contents)) != descriptor.Size {
		return fmt.Errorf("size-invalid selected descriptor %q: expected %d bytes, got %d", title, descriptor.Size, len(contents))
	}
	if actual := descriptor.Digest.Algorithm().FromBytes(contents); actual != descriptor.Digest {
		return fmt.Errorf("digest-invalid selected descriptor %q: expected %s, got %s", title, descriptor.Digest, actual)
	}
	return nil
}

func publishDescriptorBytes(target string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".descriptor-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(contents); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, target)
}
