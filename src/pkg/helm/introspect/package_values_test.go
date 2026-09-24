package introspect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/zarf-dev/zarf/src/api/v1alpha1"
)

func TestPackageValuesSourceMergeAndOperatorPrecedence(t *testing.T) {
	root := t.TempDir()
	for filename, contents := range map[string]string{
		"first.yaml":    "app:\n  name: first\n  port: 8080\nitems: [one]\n",
		"second.yaml":   "app:\n  name: second\nitems: [two]\n",
		"override.yaml": "app:\n  port: 9090\n",
		"schema.json":   `{"type":"object","properties":{"app":{"type":"object"}}}`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filename), []byte(contents), 0600))
	}
	pkg := v1alpha1.ZarfPackage{}
	pkg.Metadata.Name = "example"
	pkg.Values.Files = []string{"first.yaml", "second.yaml"}
	pkg.Values.Schema = "schema.json"
	got, err := inspectPackageValues(context.Background(), root, root, true, pkg, []string{filepath.Join(root, "override.yaml")})
	require.NoError(t, err)
	require.Equal(t, []string{"first.yaml", "second.yaml", filepath.Join(root, "override.yaml")}, got.Files)
	require.Equal(t, "second", got.Values["app"].(map[string]interface{})["name"])
	require.EqualValues(t, 9090, got.Values["app"].(map[string]interface{})["port"])
	require.Equal(t, []interface{}{"two"}, got.Values["items"])
	require.Equal(t, "object", got.Schema["type"])
}

func TestPackageValuesAssembledDescriptorsRequired(t *testing.T) {
	root := t.TempDir()
	pkg := v1alpha1.ZarfPackage{}
	pkg.Values.Files = []string{"source/default.yaml"}
	pkg.Values.Schema = "source/schema.json"
	_, err := inspectPackageValues(context.Background(), root, root, false, pkg, nil)
	require.ErrorContains(t, err, "values.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(root, "values.yaml"), []byte("foo: bar\n"), 0600))
	_, err = inspectPackageValues(context.Background(), root, root, false, pkg, nil)
	require.ErrorContains(t, err, "values.schema.json")
}

func TestPackageValueRuntimeReferencesNotDefaults(t *testing.T) {
	references := inspectTemplateText("name: '{{ .Values.app.name }}'\nother: '{{ .Values.app.replicas }}'", "values.tpl", "apps", "api")
	require.Len(t, references, 2)
	require.Equal(t, ".app.name", references[0].Path)
	require.True(t, references[0].Runtime)
	require.Equal(t, "values.tpl", references[0].Locations[0].File)
	require.Equal(t, ".app.replicas", references[1].Path)
}

func TestPackageValuesTemplateWalkerHandlesIndexAliasesAndLiterals(t *testing.T) {
	text := `{{/* .Values.comment */}} {{ "literal .Values.fake" }} {{ index .Values "app" "name" }} {{ $app := .Values.app }} {{ $app.name }}`
	found := inspectTemplateText(text, "template.yaml", "apps", "demo")
	paths := make([]string, 0, len(found))
	for _, usage := range found {
		paths = append(paths, usage.Path)
	}
	require.Contains(t, paths, ".app.name")
	require.Contains(t, paths, ".app")
	require.NotContains(t, paths, ".comment")
	require.NotContains(t, paths, ".fake")
	unresolved := inspectTemplateText("{{ invalid (", "broken.yaml", "apps", "demo")
	require.Len(t, unresolved, 1)
	require.Equal(t, "unparsedTemplate", unresolved[0].Locations[0].Kind)
}

func TestPackageReaderSelectsVerifiedPackageValuesWithoutImages(t *testing.T) {
	manifest := []byte("kind: ZarfPackageConfig\nmetadata:\n  name: example\n  version: 1.0.0\nvalues:\n  files: [source/default.yaml]\n  schema: source/schema.json\n")
	defaults := []byte("app:\n  replicas: 2\n")
	schema := []byte(`{"type":"object"}`)
	image := []byte("image")
	zarfDesc := packageDescriptor("zarf.yaml", manifest)
	valuesDesc := packageDescriptor("values.yaml", defaults)
	schemaDesc := packageDescriptor("values.schema.json", schema)
	imageDesc := packageDescriptor("images/index.json", image)
	remote := &fakePackageRemote{
		root: packageRoot{Descriptor: ocispec.Descriptor{Digest: digest.FromString("root")}, Manifest: ocispec.Manifest{
			Config: ocispec.Descriptor{MediaType: zarfConfigMediaType},
			Layers: []ocispec.Descriptor{zarfDesc, valuesDesc, schemaDesc, imageDesc},
		}},
		blobs: map[digest.Digest][]byte{zarfDesc.Digest: manifest, valuesDesc.Digest: defaults, schemaDesc.Digest: schema, imageDesc.Digest: image},
	}
	reader := newPackageReader(remote, ocispec.Platform{Architecture: "amd64"}, t.TempDir(), true)
	view, err := reader.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"zarf.yaml", "values.yaml", "values.schema.json"}, remote.fetched)
	require.Equal(t, defaults, view.Files["values.yaml"])
	require.Equal(t, schema, view.Files["values.schema.json"])
	delete(remote.blobs, valuesDesc.Digest)
	_, err = reader.Read(context.Background())
	require.ErrorContains(t, err, "values.yaml")
	remote.root.Manifest.Layers = []ocispec.Descriptor{zarfDesc, schemaDesc, imageDesc}
	_, err = reader.Read(context.Background())
	require.ErrorContains(t, err, "missing selected descriptor \"values.yaml\"")
}

func TestInspectZarfPackageKeepsLegacyAndPackageValuesSeparate(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "components", "apps", "charts", "demo")
	require.NoError(t, os.MkdirAll(filepath.Join(chart, "templates"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: demo\nversion: 1.0.0\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "values.yaml"), []byte("replicas: 1\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "templates", "app.yaml"), []byte("replicas: {{ .Values.replicas }}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "defaults.yaml"), []byte("app:\n  replicas: 2\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "template.yaml"), []byte("replicas: {{ .Values.app.replicas }}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "zarf.yaml"), []byte(`kind: ZarfPackageConfig
metadata:
  name: mixed
  version: 1.0.0
values:
  files: [defaults.yaml]
variables:
  - name: LEGACY
    default: retained
components:
  - name: apps
    charts:
      - name: demo
        version: 1.0.0
        templatedValuesFiles: [template.yaml]
        values:
          - sourcePath: .app
            targetPath: .
            excludePaths: [.app.private]
`), 0600))
	report, err := InspectZarfPackage(context.Background(), root, "", "amd64", IntrospectOptions{})
	require.NoError(t, err)
	require.Len(t, report.Variables, 1)
	require.Equal(t, "LEGACY", report.Variables[0].Name)
	require.Len(t, report.PackageValues, 1)
	require.EqualValues(t, 2, report.PackageValues[0].Values["app"].(map[string]interface{})["replicas"])
	mappings := report.ChartResults["apps/demo"].PackageValueMappings
	require.Len(t, mappings, 1)
	require.Equal(t, ".app", mappings[0].Path)
	require.Equal(t, ".", mappings[0].TargetPath)
	require.Equal(t, []string{".app.private"}, mappings[0].ExcludePaths)
	require.Equal(t, []string{"values.yaml"}, report.ChartResults["apps/demo"].ValuesFiles)
	var runtimePaths []string
	for _, usage := range report.PackageValues[0].Usages {
		if usage.Runtime {
			runtimePaths = append(runtimePaths, usage.Path)
		}
	}
	require.Contains(t, runtimePaths, ".app.replicas")
}

func TestLegacyBundleReadsAssembledPackageValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg")
	require.NoError(t, os.Mkdir(pkgDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "zarf.yaml"), []byte("kind: ZarfPackageConfig\nmetadata:\n  name: original\nvalues:\n  files: [source-only.yaml]\nvariables:\n  - name: LEGACY\n    default: kept\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "values.yaml"), []byte("app:\n  enabled: false\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, UDSBundleFile), []byte("kind: UDSBundle\nmetadata:\n  name: legacy\npackages:\n  - name: bundled\n    path: "+pkgDir+"\n"), 0600))
	report, err := inspectMaterializedUDSBundle(context.Background(), root, "", "amd64", IntrospectOptions{}, true)
	require.NoError(t, err)
	require.Len(t, report.PackageValues, 1)
	require.Equal(t, "bundled", report.PackageValues[0].PackageName)
	require.Equal(t, false, report.PackageValues[0].Values["app"].(map[string]interface{})["enabled"])
	require.Len(t, report.Variables, 1)
	require.Equal(t, "bundled.LEGACY", report.Variables[0].Name)
}
