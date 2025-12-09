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

func TestWalker_ScopeWildcards(t *testing.T) {
	tests := []struct {
		name         string
		template     string
		wantPath     string
		dontWantPatn string
	}{
		{
			name:         "With block should NOT add wildcard",
			template:     `{{ with .Values.probes.readiness }} {{ .timeoutSeconds }} {{ end }}`,
			wantPath:     "probes.readiness.timeoutSeconds",
			dontWantPatn: "probes.readiness[].timeoutSeconds",
		},
		{
			name:         "Range block SHOULD add wildcard",
			template:     `{{ range .Values.items }} {{ .name }} {{ end }}`,
			wantPath:     "items[].name",
			dontWantPatn: "items.name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.New("test")
			parsed, err := tmpl.Parse(tt.template)
			require.NoError(t, err)

			w := newWalker("testchart", "test.yaml", tt.template)
			w.walk(parsed.Tree.Root)

			foundWant := false
			foundDontWant := false
			for _, res := range w.Results {
				if res.Path == tt.wantPath {
					foundWant = true
				}
				if res.Path == tt.dontWantPatn {
					foundDontWant = true
				}
			}
			assert.True(t, foundWant, "Expected to find usage of %s", tt.wantPath)
			assert.False(t, foundDontWant, "Expected NOT to find usage of %s", tt.dontWantPatn)
		})
	}
}

func TestWalker_Variables(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantPath string
		dontWant string
	}{
		{
			name:     "Direct variable assignment",
			template: `{{ $foo := .Values.bar }}{{ $foo }}`,
			wantPath: "bar",
			dontWant: "Values.bar",
		},
		{
			name:     "Variable from scope",
			template: `{{ with .Values.baz }}{{ $val := . }}{{ $val }}{{ end }}`,
			wantPath: "baz",
			dontWant: "Values.baz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.New("test")
			parsed, err := tmpl.Parse(tt.template)
			require.NoError(t, err)

			w := newWalker("testchart", "test.yaml", tt.template)
			w.walk(parsed.Tree.Root)

			foundWant := false
			foundDontWant := false
			for _, res := range w.Results {
				if res.Path == tt.wantPath {
					foundWant = true
				}
				if res.Path == tt.dontWant {
					foundDontWant = true
				}
			}
			assert.True(t, foundWant, "Expected to find usage of %s", tt.wantPath)
			assert.False(t, foundDontWant, "Expected NOT to find usage of %s", tt.dontWant)
		})
	}
}
