package introspect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zarf-dev/zarf/src/api"
	"github.com/zarf-dev/zarf/src/pkg/value"
)

func TestValueSourceTraceMatchesPinnedZarfMerge(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for _, doc := range []struct{ name, text string }{
		{"defaults.yaml", "object: {retained: true, replaced: old}\nscalar: old\nlist: [one, two]\nnull: before\nerased: {child: before}\n"},
		{"second.yaml", "object: {replaced: new}\nscalar: {child: object}\nlist: [three]\nnull: null\nerased: null\n"},
		{"operator.yaml", "object: {}\nscalar: scalar-again\nlist: []\ncreated: {child: later}\n"},
	} {
		path := filepath.Join(root, doc.name)
		require.NoError(t, os.WriteFile(path, []byte(doc.text), 0600))
		paths = append(paths, path)
	}
	pkg := api.Package{}
	pkg.Values.Files = []string{"defaults.yaml", "second.yaml"}
	got, err := inspectPackageValues(context.Background(), root, root, true, pkg, paths[2:])
	require.NoError(t, err)
	pinned, err := value.ParseFiles(context.Background(), paths, value.ParseFilesOptions{})
	require.NoError(t, err)
	require.Equal(t, map[string]interface{}(pinned), got.Values)
	trace := TraceValueSources(got.Sources, ".")
	require.Equal(t, map[string]interface{}(pinned), trace.Value)
	require.Equal(t, "old", got.Sources[0].Values["object"].(map[string]interface{})["replaced"], "later merges must not mutate source snapshots")
	for _, path := range []string{".object", ".object.retained", ".scalar", ".list", ".null", ".erased.child", ".created.child"} {
		traced := TraceValueSources(got.Sources, path)
		want, extractErr := pinned.Extract(value.Path(path))
		require.Equal(t, extractErr == nil, traced.Present, path)
		if extractErr == nil {
			require.Equal(t, want, traced.Value, path)
		}
	}
}
