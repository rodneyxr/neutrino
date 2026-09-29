package introspect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zclconf/go-cty/cty"
)

const (
	nextBundleFile        = "bundle.uds.hcl"
	nextDefaultsFile      = "defaults.uds.hcl"
	nextBundleType        = "application/vnd.defenseunicorns.uds.bundle.v1"
	nextDefinitionType    = "application/vnd.defenseunicorns.uds.bundle.definition.v1"
	nextHCLType           = "application/vnd.defenseunicorns.uds.bundle.hcl.v1"
	nextValuesType        = "application/vnd.defenseunicorns.uds.bundle.values.v1+yaml"
	nextPackageAnnotation = "uds.dev/package.name"
	nextArchAnnotation    = "uds.dev/architecture"
	nextMaxControlBytes   = 8 << 20
)

type nextDefinition struct {
	UDS struct {
		Version string `hcl:"bundle_api_version"`
	} `hcl:"uds,block"`
	Metadata struct {
		Name        string `hcl:"name"`
		Version     string `hcl:"version,optional"`
		Description string `hcl:"description,optional"`
	} `hcl:"metadata,block"`
	Packages []nextPackage `hcl:"package,block"`
	Remain   hcl.Body      `hcl:",remain"`
}
type nextSignature struct {
	Verify    *bool    `hcl:"verify,optional"`
	PublicKey string   `hcl:"public_key,optional"`
	Remain    hcl.Body `hcl:",remain"`
}
type nextPackage struct {
	ID                 string         `hcl:"name,label"`
	Source             string         `hcl:"source"`
	Namespace          string         `hcl:"namespace,optional"`
	ValuesFiles        []string       `hcl:"values_files,optional"`
	OptionalComponents []string       `hcl:"optional_components,optional"`
	Remain             hcl.Body       `hcl:",remain"`
	Signature          *nextSignature `hcl:"signature_verification,block"`
	DependsOn          []string
}

func nextEval(src []byte, name, arch string) (*hcl.File, *hcl.EvalContext, error) {
	file, diags := hclsyntax.ParseConfig(src, name, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("parse %s: %w", name, diags)
	}
	body := file.Body.(*hclsyntax.Body)
	expressions := map[string]hcl.Expression{}
	for _, block := range body.Blocks {
		if block.Type == "locals" {
			for key, attr := range block.Body.Attributes {
				if _, exists := expressions[key]; exists {
					return nil, nil, fmt.Errorf("duplicate local %q", key)
				}
				expressions[key] = attr.Expr
			}
		}
	}
	values := map[string]cty.Value{}
	pending := len(expressions)
	for pending > 0 {
		changed := false
		for key, expr := range expressions {
			if _, ok := values[key]; ok {
				continue
			}
			ready := true
			for _, ref := range expr.Variables() {
				if len(ref) < 2 || ref.RootName() != "local" {
					continue
				}
				part, ok := ref[1].(hcl.TraverseAttr)
				if !ok {
					return nil, nil, fmt.Errorf("unsupported local reference in %q", key)
				}
				if _, ok = expressions[part.Name]; !ok {
					return nil, nil, fmt.Errorf("unknown local %q", part.Name)
				}
				if _, ok = values[part.Name]; !ok {
					ready = false
				}
			}
			if !ready {
				continue
			}
			ctx := nextContext(values, arch)
			value, diags := expr.Value(ctx)
			if diags.HasErrors() {
				return nil, nil, fmt.Errorf("local %q: %w", key, diags)
			}
			values[key] = value
			pending--
			changed = true
		}
		if !changed {
			return nil, nil, fmt.Errorf("cyclic local references in %s", name)
		}
	}
	return file, nextContext(values, arch), nil
}
func nextContext(locals map[string]cty.Value, arch string) *hcl.EvalContext {
	if arch == "" {
		arch = runtime.GOARCH
	}
	local := cty.EmptyObjectVal
	if len(locals) > 0 {
		local = cty.ObjectVal(locals)
	}
	return &hcl.EvalContext{Variables: map[string]cty.Value{"local": local, "sys": cty.ObjectVal(map[string]cty.Value{"arch": cty.StringVal(arch)})}}
}
func parseNextDefinition(data []byte, arch string) (*nextDefinition, error) {
	file, ctx, err := nextEval(data, nextBundleFile, arch)
	if err != nil {
		return nil, err
	}
	var def nextDefinition
	_, remainder, diags := file.Body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "locals"}}})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse %s locals: %w", nextBundleFile, diags)
	}
	if diags := gohcl.DecodeBody(remainder, ctx, &def); diags.HasErrors() {
		return nil, fmt.Errorf("decode %s: %w", nextBundleFile, diags)
	}
	if def.UDS.Version != "uds.dev/v1alpha1" {
		return nil, fmt.Errorf("unsupported bundle_api_version %q", def.UDS.Version)
	}
	if def.Metadata.Name == "" {
		return nil, fmt.Errorf("bundle metadata.name is required")
	}
	body := file.Body.(*hclsyntax.Body)
	for key := range body.Attributes {
		return nil, fmt.Errorf("unexpected bundle attribute %q", key)
	}
	for _, block := range body.Blocks {
		if block.Type != "uds" && block.Type != "metadata" && block.Type != "locals" && block.Type != "package" {
			return nil, fmt.Errorf("unexpected bundle block %q", block.Type)
		}
	}
	packageIndex := 0
	for _, block := range body.Blocks {
		if block.Type != "package" {
			continue
		}
		pkg := &def.Packages[packageIndex]
		packageIndex++
		if pkg.ID == "" || pkg.Source == "" {
			return nil, fmt.Errorf("package %q requires source and ID", pkg.ID)
		}
		for key := range block.Body.Attributes {
			switch key {
			case "source", "namespace", "values_files", "optional_components", "depends_on":
			default:
				return nil, fmt.Errorf("package %q: unknown field %q", pkg.ID, key)
			}
		}
		for _, child := range block.Body.Blocks {
			if child.Type != "signature_verification" {
				return nil, fmt.Errorf("package %q: unknown block %q", pkg.ID, child.Type)
			}
		}
		attr := block.Body.Attributes["depends_on"]
		if attr != nil {
			expr, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
			if !ok {
				return nil, fmt.Errorf("package %q depends_on must be a list of package references", pkg.ID)
			}
			for _, item := range expr.Exprs {
				traversal, diags := hcl.AbsTraversalForExpr(item)
				if diags.HasErrors() || len(traversal) != 2 || traversal.RootName() != "package" {
					return nil, fmt.Errorf("package %q has invalid depends_on reference", pkg.ID)
				}
				ref, ok := traversal[1].(hcl.TraverseAttr)
				if !ok {
					return nil, fmt.Errorf("package %q has invalid depends_on reference", pkg.ID)
				}
				pkg.DependsOn = append(pkg.DependsOn, ref.Name)
			}
		}
	}
	return &def, nil
}
func nextPackageOrder(def *nextDefinition) ([]nextPackage, error) {
	indexed := map[string]nextPackage{}
	for _, pkg := range def.Packages {
		if _, ok := indexed[pkg.ID]; ok {
			return nil, fmt.Errorf("duplicate bundle package %q", pkg.ID)
		}
		indexed[pkg.ID] = pkg
	}
	state := map[string]int{}
	ordered := make([]nextPackage, 0, len(def.Packages))
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("bundle package dependency cycle at %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		pkg, ok := indexed[id]
		if !ok {
			return fmt.Errorf("unknown bundle package dependency %q", id)
		}
		state[id] = 1
		for _, dep := range pkg.DependsOn {
			if err := visit(dep); err != nil {
				return fmt.Errorf("package %q: %w", id, err)
			}
		}
		state[id] = 2
		ordered = append(ordered, pkg)
		return nil
	}
	for _, pkg := range def.Packages {
		if err := visit(pkg.ID); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
func nextSafePath(p string) bool {
	return p != "" && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && !strings.Contains(p, "\\")
}
func nextReadControl(file string) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > nextMaxControlBytes {
		return nil, fmt.Errorf("invalid or oversized bundle control file %q", file)
	}
	return os.ReadFile(file)
}
func nextVariables(data []byte, name string) (map[string]interface{}, error) {
	file, diags := hclsyntax.ParseConfig(data, name, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse %s: %w", name, diags)
	}
	body := file.Body.(*hclsyntax.Body)
	for _, block := range body.Blocks {
		if name == nextDefaultsFile || block.Type != "options" && block.Type != "signature_verification" {
			return nil, fmt.Errorf("unexpected block %q in %s", block.Type, name)
		}
	}
	for key := range body.Attributes {
		if key != "variables" {
			return nil, fmt.Errorf("unexpected attribute %q in %s", key, name)
		}
	}
	attr := body.Attributes["variables"]
	if attr == nil {
		return nil, nil
	}
	value, diags := attr.Expr.Value(&hcl.EvalContext{})
	if diags.HasErrors() {
		return nil, fmt.Errorf("evaluate %s variables: %w", name, diags)
	}
	converted, err := nextConvert(value)
	if err != nil {
		return nil, err
	}
	result, ok := converted.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s variables must be an object", name)
	}
	return result, nil
}
func nextConvert(value cty.Value) (interface{}, error) {
	if !value.IsKnown() || value.IsNull() {
		return nil, fmt.Errorf("unknown or null bundle variable")
	}
	typ := value.Type()
	switch {
	case typ == cty.String:
		return value.AsString(), nil
	case typ == cty.Bool:
		return value.True(), nil
	case typ == cty.Number:
		number, _ := value.AsBigFloat().Float64()
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("bundle variable number is outside float64 range")
		}
		return number, nil
	case typ.IsObjectType():
		result := make(map[string]interface{}, len(typ.AttributeTypes()))
		for key := range typ.AttributeTypes() {
			converted, err := nextConvert(value.GetAttr(key))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			result[key] = converted
		}
		return result, nil
	case typ.IsTupleType(), typ.IsListType(), typ.IsSetType():
		result := make([]interface{}, 0, value.LengthInt())
		items := value.ElementIterator()
		for items.Next() {
			_, item := items.Element()
			converted, err := nextConvert(item)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported bundle variable type %s", typ.FriendlyName())
	}
}

func nextMerge(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = map[string]interface{}{}
	}
	for key, val := range src {
		if incoming, ok := val.(map[string]interface{}); ok {
			if existing, ok := dst[key].(map[string]interface{}); ok {
				dst[key] = nextMerge(existing, incoming)
				continue
			}
		}
		dst[key] = val
	}
	return dst
}

// InspectNextUDSBundle statically inspects a Next source definition or created artifact.
func InspectNextUDSBundle(ctx context.Context, source, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	if IsRemote(source) {
		return inspectNextRemote(ctx, source, version, arch, opts)
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(source, "oci", "index.json")); err == nil {
			return inspectNextLayout(ctx, source, source, version, arch, opts)
		}
		if _, err := os.Stat(filepath.Join(source, nextBundleFile)); err == nil {
			return inspectNextSource(ctx, source, version, arch, opts)
		}
		return nil, fmt.Errorf("directory %q is not a Next bundle source or artifact", source)
	}
	if filepath.Base(source) == nextBundleFile {
		return inspectNextSource(ctx, filepath.Dir(source), version, arch, opts)
	}
	return inspectNextArchive(ctx, source, version, arch, opts)
}
func inspectNextSource(ctx context.Context, root, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	data, err := nextReadControl(filepath.Join(root, nextBundleFile))
	if err != nil {
		return nil, err
	}
	def, err := parseNextDefinition(data, arch)
	if err != nil {
		return nil, err
	}
	order, err := nextPackageOrder(def)
	if err != nil {
		return nil, err
	}
	return inspectNextPackages(ctx, def, order, root, nil, version, arch, opts)
}
func inspectNextLayout(ctx context.Context, root, source, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	layout := filepath.Join(root, "oci")
	idxBytes, err := nextReadControl(filepath.Join(layout, "index.json"))
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
		blob := filepath.Join(layout, "blobs", string(d.Digest.Algorithm()), d.Digest.Encoded())
		info, err := os.Stat(blob)
		if err != nil {
			return nil, err
		}
		if info.Size() != d.Size {
			return nil, fmt.Errorf("size mismatch for %s", d.Digest)
		}
		data, err := os.ReadFile(blob)
		if err != nil {
			return nil, err
		}
		if err := verifyDescriptorBytes(d, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return inspectNextIndex(ctx, idxBytes, source, version, arch, opts, fetch, root)
}
func inspectNextRemote(ctx context.Context, source, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	platform := oci.PlatformForArch(config.GetArch(arch))
	remote, err := oci.NewOrasRemote(source, platform)
	if err != nil {
		return nil, fmt.Errorf("open Next bundle %q: %w", source, err)
	}
	descriptor, err := remote.Repo().Resolve(ctx, remote.Repo().Reference.Reference)
	if err != nil {
		return nil, fmt.Errorf("resolve Next bundle %q: %w", source, err)
	}
	if descriptor.MediaType != ocispec.MediaTypeImageIndex {
		return nil, fmt.Errorf("Next bundle %q must resolve to an OCI index", source)
	}
	fetch := func(ctx context.Context, d ocispec.Descriptor) ([]byte, error) { return remote.FetchLayer(ctx, d) }
	data, err := nextFetch(ctx, fetch, descriptor)
	if err != nil {
		return nil, err
	}
	return inspectNextIndex(ctx, data, source, version, arch, opts, fetch, "")
}
func nextFetch(ctx context.Context, fetch func(context.Context, ocispec.Descriptor) ([]byte, error), desc ocispec.Descriptor) ([]byte, error) {
	if desc.Size < 0 || desc.Size > nextMaxControlBytes {
		return nil, fmt.Errorf("oversized bundle metadata descriptor %s", desc.Digest)
	}
	if err := desc.Digest.Validate(); err != nil {
		return nil, err
	}
	data := desc.Data
	if data == nil {
		var err error
		data, err = fetch(ctx, desc)
		if err != nil {
			return nil, err
		}
	}
	if err := verifyDescriptorBytes(desc, data); err != nil {
		return nil, err
	}
	return data, nil
}
func inspectNextIndex(ctx context.Context, indexBytes []byte, source, version, arch string, opts IntrospectOptions, fetch func(context.Context, ocispec.Descriptor) ([]byte, error), root string) (*ZarfPackageIntrospection, error) {
	var idx ocispec.Index
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return nil, fmt.Errorf("parse Next bundle index: %w", err)
	}
	if idx.ArtifactType != nextBundleType { // Published tags use a multi-architecture index around the canonical child.
		var children []ocispec.Descriptor
		target := arch
		if target == "" {
			target = runtime.GOARCH
		}
		for _, d := range idx.Manifests {
			if d.MediaType == ocispec.MediaTypeImageIndex && d.ArtifactType == nextBundleType && d.Platform != nil && d.Platform.Architecture == target {
				children = append(children, d)
			}
		}
		if len(children) != 1 {
			return nil, fmt.Errorf("Next bundle index has %d children for architecture %q (expected one)", len(children), target)
		}
		bytes, err := nextFetch(ctx, fetch, children[0])
		if err != nil {
			return nil, err
		}
		return inspectNextIndex(ctx, bytes, source, version, arch, opts, fetch, root)
	}
	if idx.SchemaVersion != 2 || idx.MediaType != ocispec.MediaTypeImageIndex {
		return nil, fmt.Errorf("unsupported Next bundle OCI index")
	}
	if selected := idx.Annotations[nextArchAnnotation]; selected != "" && arch != "" && selected != arch {
		return nil, fmt.Errorf("bundle architecture %q does not match %q", selected, arch)
	}
	var definition ocispec.Descriptor
	packages := map[string]ocispec.Descriptor{}
	for _, entry := range idx.Manifests {
		if entry.ArtifactType == nextDefinitionType {
			if definition.Digest != "" {
				return nil, fmt.Errorf("duplicate Next bundle definitions")
			}
			definition = entry
			continue
		}
		id := entry.Annotations[nextPackageAnnotation]
		if id == "" || entry.MediaType != ocispec.MediaTypeImageManifest {
			return nil, fmt.Errorf("unexpected Next bundle index entry %s without package identity", entry.Digest)
		}
		if _, exists := packages[id]; exists {
			return nil, fmt.Errorf("duplicate embedded package %q", id)
		}
		packages[id] = entry
	}
	if definition.Digest == "" {
		return nil, fmt.Errorf("missing Next bundle definition manifest")
	}
	bytes, err := nextFetch(ctx, fetch, definition)
	if err != nil {
		return nil, err
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse Next definition manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 || manifest.MediaType != ocispec.MediaTypeImageManifest {
		return nil, fmt.Errorf("unsupported Next definition manifest")
	}
	if manifest.Config.MediaType != "application/vnd.oci.empty.v1+json" {
		return nil, fmt.Errorf("unsupported Next definition config media type %q", manifest.Config.MediaType)
	}
	if _, err := nextFetch(ctx, fetch, manifest.Config); err != nil {
		return nil, fmt.Errorf("verify Next definition config: %w", err)
	}
	controls := map[string][]byte{}
	for _, layer := range manifest.Layers {
		title := layer.Annotations[ocispec.AnnotationTitle]
		if !nextSafePath(title) || (layer.MediaType != nextHCLType && layer.MediaType != nextValuesType) {
			return nil, fmt.Errorf("unsupported Next control layer %q (%s)", title, layer.MediaType)
		}
		if (title == nextBundleFile || title == nextDefaultsFile) != (layer.MediaType == nextHCLType) {
			return nil, fmt.Errorf("Next control layer %q has inconsistent media type %q", title, layer.MediaType)
		}
		if _, exists := controls[title]; exists {
			return nil, fmt.Errorf("duplicate Next control layer %q", title)
		}
		data, err := nextFetch(ctx, fetch, layer)
		if err != nil {
			return nil, fmt.Errorf("read Next control layer %q: %w", title, err)
		}
		controls[title] = data
	}
	if len(controls[nextBundleFile]) == 0 {
		return nil, fmt.Errorf("missing %s layer", nextBundleFile)
	}
	parseArch := idx.Annotations[nextArchAnnotation]
	if parseArch == "" {
		parseArch = arch
	}
	def, err := parseNextDefinition(controls[nextBundleFile], parseArch)
	if err != nil {
		return nil, err
	}
	order, err := nextPackageOrder(def)
	if err != nil {
		return nil, err
	}
	if len(packages) != len(order) {
		return nil, fmt.Errorf("Next bundle declares %d packages but contains %d package descriptors", len(order), len(packages))
	}
	for _, pkg := range order {
		if _, ok := packages[pkg.ID]; !ok {
			return nil, fmt.Errorf("missing embedded package %q", pkg.ID)
		}
		for i := range pkg.ValuesFiles {
			title := fmt.Sprintf("values/%s/%d.yaml", pkg.ID, i)
			if _, ok := controls[title]; !ok {
				return nil, fmt.Errorf("missing values layer %q", title)
			}
		}
	}
	remoteBundle := root == "" && IsRemote(source)
	root, err = getPackageViewDir(source, digest.FromBytes(indexBytes).String())
	if err != nil {
		return nil, err
	}
	controlRoot := filepath.Join(root, "next-controls")
	for title, data := range controls {
		target := filepath.Join(controlRoot, filepath.FromSlash(title))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return nil, err
		}
	}
	blobCache, err := getCacheDir("package-blobs", "verified", "")
	if err != nil {
		return nil, err
	}
	reader := newPackageReader(resolvedPackageRemote{fetch: fetch}, oci.PlatformForArch(config.GetArch(arch)), blobCache, opts.NoCache)
	paths := map[string]string{}
	for _, pkg := range order {
		var view *packageView
		if remoteBundle && strings.HasPrefix(pkg.Source, "oci://") {
			view, err = readNextOCIPackage(ctx, pkg.Source, packages[pkg.ID], fetch, oci.PlatformForArch(config.GetArch(arch)), blobCache, opts.NoCache)
		} else {
			view, err = reader.ReadEmbedded(ctx, packages[pkg.ID], fetch)
		}
		if err != nil {
			return nil, fmt.Errorf("package %q Package View: %w", pkg.ID, err)
		}
		target := filepath.Join(root, "next-packages", pkg.ID)
		if !nextSafePath(pkg.ID) {
			return nil, fmt.Errorf("unsafe package ID %q", pkg.ID)
		}
		if err := view.materialize(target); err != nil {
			return nil, err
		}
		paths[pkg.ID] = target
	}
	return inspectNextPackages(ctx, def, order, controlRoot, paths, version, arch, opts)
}

func readNextOCIPackage(ctx context.Context, source string, embeddedDescriptor ocispec.Descriptor, bundleFetch func(context.Context, ocispec.Descriptor) ([]byte, error), platform ocispec.Platform, blobCache string, noCache bool) (*packageView, error) {
	embeddedBytes, err := nextFetch(ctx, bundleFetch, embeddedDescriptor)
	if err != nil {
		return nil, fmt.Errorf("read embedded package manifest: %w", err)
	}
	var embedded ocispec.Manifest
	if err := json.Unmarshal(embeddedBytes, &embedded); err != nil {
		return nil, fmt.Errorf("parse embedded package manifest: %w", err)
	}
	remote := newRegistryPackageRemote(source)
	sourceRoot, err := remote.Resolve(ctx, platform)
	if err != nil {
		return nil, fmt.Errorf("resolve package source %q: %w", source, err)
	}
	if embedded.SchemaVersion != 2 || embedded.MediaType != ocispec.MediaTypeImageManifest ||
		embedded.Config.Digest != sourceRoot.Manifest.Config.Digest ||
		embedded.Config.Size != sourceRoot.Manifest.Config.Size ||
		embedded.Config.MediaType != sourceRoot.Manifest.Config.MediaType {
		return nil, fmt.Errorf("package source %q config differs from embedded package", source)
	}
	sourceLayers := make(map[string]ocispec.Descriptor, len(sourceRoot.Manifest.Layers))
	for _, d := range sourceRoot.Manifest.Layers {
		title := d.Annotations[ocispec.AnnotationTitle]
		if title != "" {
			if _, exists := sourceLayers[title]; exists {
				return nil, fmt.Errorf("package source %q has duplicate descriptor title %q", source, title)
			}
			sourceLayers[title] = d
		}
	}
	hasZarf := false
	for _, d := range embedded.Layers {
		title := d.Annotations[ocispec.AnnotationTitle]
		if title == "" {
			continue
		}
		sourceDescriptor, exists := sourceLayers[title]
		if !exists || d.Digest != sourceDescriptor.Digest || d.Size != sourceDescriptor.Size || d.MediaType != sourceDescriptor.MediaType {
			return nil, fmt.Errorf("package source %q descriptor %q differs from embedded package", source, title)
		}
		if title == "zarf.yaml" {
			hasZarf = true
		}
	}
	if !hasZarf {
		return nil, fmt.Errorf("embedded package lacks zarf.yaml descriptor for source %q", source)
	}
	return newPackageReader(resolvedPackageRemote{root: sourceRoot, fetch: remote.Fetch}, platform, blobCache, noCache).Read(ctx)
}

func inspectNextPackages(ctx context.Context, def *nextDefinition, order []nextPackage, root string, paths map[string]string, version, arch string, opts IntrospectOptions) (*ZarfPackageIntrospection, error) {
	defaults := map[string]interface{}{}
	if data, err := nextReadControl(filepath.Join(root, nextDefaultsFile)); err == nil {
		defaults, err = nextVariables(data, nextDefaultsFile)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if opts.BundleConfig != "" {
		data, err := nextReadControl(opts.BundleConfig)
		if err != nil {
			return nil, fmt.Errorf("bundle config: %w", err)
		}
		vars, err := nextVariables(data, opts.BundleConfig)
		if err != nil {
			return nil, err
		}
		defaults = nextMerge(defaults, vars)
	}
	result := &ZarfPackageIntrospection{ChartResults: map[string]*IntrospectResult{}, Bundle: &BundleMetadata{Format: "next", Name: def.Metadata.Name, Version: def.Metadata.Version, Variables: defaults}}
	for _, pkg := range order {
		source := pkg.Source
		if paths != nil {
			source = paths[pkg.ID]
		} else if !IsRemote(source) && !filepath.IsAbs(source) {
			source = filepath.Join(root, source)
		}
		values := make([]string, 0, len(pkg.ValuesFiles)+len(opts.ValuesFiles))
		var pending []string
		for i, name := range pkg.ValuesFiles {
			if paths != nil {
				name = fmt.Sprintf("values/%s/%d.yaml", pkg.ID, i)
			}
			if paths != nil && !nextSafePath(name) {
				return nil, fmt.Errorf("unsafe values path %q", name)
			}
			if !filepath.IsAbs(name) {
				name = filepath.Join(root, name)
			}
			data, err := nextReadControl(name)
			if err != nil {
				return nil, fmt.Errorf("package %q values file: %w", pkg.ID, err)
			}
			if strings.Contains(string(data), "{{") {
				tmpl, err := template.New(name).Option("missingkey=error").Parse(string(data))
				if err != nil {
					return nil, fmt.Errorf("package %q template %q: %w", pkg.ID, name, err)
				}
				var rendered bytes.Buffer
				if err := tmpl.Execute(&rendered, map[string]interface{}{"vars": defaults}); err != nil {
					pending = append(pending, name)
					continue
				}
				viewDir, err := getPackageViewDir(source, fmt.Sprintf("values-%s-%d-%x", pkg.ID, i, digest.FromBytes(rendered.Bytes())))
				if err != nil {
					return nil, err
				}
				materialized := filepath.Join(viewDir, "bundle-values.yaml")
				if err := os.MkdirAll(viewDir, 0700); err != nil {
					return nil, err
				}
				if err := os.WriteFile(materialized, rendered.Bytes(), 0600); err != nil {
					return nil, err
				}
				name = materialized
			}
			values = append(values, name)
		}
		values = append(values, opts.ValuesFiles...)
		pkgOpts := opts
		pkgOpts.ValuesFiles = values
		pkgOpts.BundleConfig = ""
		var inspected *ZarfPackageIntrospection
		var err error
		if paths != nil {
			inspected, err = inspectLocalZarfPackage(ctx, source, version, arch, pkgOpts, true)
		} else {
			inspected, err = InspectZarfPackage(ctx, source, version, arch, pkgOpts)
		}
		if err != nil {
			return nil, fmt.Errorf("bundle package %q from %q: %w", pkg.ID, source, err)
		}
		if paths != nil {
			expected, err := declaredChartCount(source)
			if err != nil {
				return nil, err
			}
			if len(inspected.ComponentOrder) != expected {
				return nil, fmt.Errorf("bundle package %q inspected %d of %d charts", pkg.ID, len(inspected.ComponentOrder), expected)
			}
		}
		for _, key := range inspected.ComponentOrder {
			if chart, ok := inspected.ChartResults[key]; ok {
				newKey := pkg.ID + "/" + key
				result.ChartResults[newKey] = chart
				result.ComponentOrder = append(result.ComponentOrder, newKey)
			}
		}
		for _, variable := range inspected.Variables {
			variable.Name = pkg.ID + "." + variable.Name
			result.Variables = append(result.Variables, variable)
		}
		if len(inspected.PackageValues) == 0 && len(pending) > 0 {
			inspected.PackageValues = []PackageValues{{PackageName: pkg.ID}}
		}
		for _, item := range inspected.PackageValues {
			item.PackageName = pkg.ID
			prefix := len(item.Files) - len(values)
			if prefix < 0 {
				prefix = 0
			}
			files := append([]string(nil), item.Files[:prefix]...)
			for i, name := range pkg.ValuesFiles {
				if paths != nil {
					name = fmt.Sprintf("values/%s/%d.yaml", pkg.ID, i)
				}
				if !filepath.IsAbs(name) {
					name = filepath.Join(root, name)
				}
				files = append(files, name)
			}
			files = append(files, opts.ValuesFiles...)
			item.Files = files
			for _, filename := range pending {
				item.Usages = append(item.Usages, PackageValueUsage{Path: filename, Runtime: true, Locations: []ValueUsageLocation{{File: filename, Kind: "bundleValuesTemplate"}}})
			}
			result.PackageValues = append(result.PackageValues, item)
		}
		result.Bundle.Packages = append(result.Bundle.Packages, BundlePackageMetadata{ID: pkg.ID, Source: pkg.Source, DependsOn: pkg.DependsOn, ValuesFiles: pkg.ValuesFiles, Namespace: pkg.Namespace, OptionalComponents: pkg.OptionalComponents})
	}
	return result, nil
}
