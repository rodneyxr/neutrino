package introspect

import (
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalker_Branches(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantPath string
	}{
		{
			name:     "If condition",
			template: `{{ if .Values.enabled }} block {{ end }}`,
			wantPath: "enabled",
		},
		{
			name:     "With condition",
			template: `{{ with .Values.config }} {{ . }} {{ end }}`,
			wantPath: "config",
		},
		{
			name:     "Range condition",
			template: `{{ range .Values.items }} {{ . }} {{ end }}`,
			wantPath: "items",
		},
		{
			name:     "If Else If condition",
			template: `{{ if .Values.a }} a {{ else if .Values.b }} b {{ end }}`,
			wantPath: "b", // We effectively want both, checking b ensures else-if traversal or at least list traversal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.New("test")
			parsed, err := tmpl.Parse(tt.template)
			require.NoError(t, err)

			w := newWalker("testchart", "test.yaml", tt.template)
			w.walk(parsed.Tree.Root)

			found := false
			for _, res := range w.Results {
				if res.Path == tt.wantPath {
					found = true
					break
				}
			}
			assert.True(t, found, "Expected to find usage of %s", tt.wantPath)
		})
	}
}
