package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func TestDetailViewBTogglesBundleMode(t *testing.T) {
	m := Model{viewMode: ViewModeDetail}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	got := updated.(Model)

	if !got.detailBundleMode {
		t.Fatal("expected b to enable bundle mode in detail view")
	}
}

func TestGenerateBundleSnippetRequiresBundleContextByDefault(t *testing.T) {
	snippet := generateBundleSnippet("component/chart", "image.tag", "1.0.0")
	if snippet != "" {
		t.Fatalf("expected no snippet outside UDS bundle context, got:\n%s", snippet)
	}
}

func TestGenerateBundleSnippetForZarfPackageBundleMode(t *testing.T) {
	m := Model{detailBundleMode: true}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path:         "image.tag",
			DefaultValue: "1.0.0",
		},
		Component: "podinfo/podinfo",
	}

	snippet := m.generateBundleSnippetForItem(v)
	for _, want := range []string{
		"- name: <package-name>",
		"      podinfo:",
		"        podinfo:",
		"            - name: <prefix>_IMAGE_TAG",
		"              path: image.tag",
		"              default: 1.0.0",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("expected snippet to contain %q, got:\n%s", want, snippet)
		}
	}
}

func TestGenerateBundleSnippetForHelmChartBundleModeUsesChartName(t *testing.T) {
	m := Model{
		detailBundleMode: true,
		components: map[string]*introspect.IntrospectResult{
			"": {ChartName: "istiod"},
		},
	}
	v := DisplayItem{
		ValueUsage: introspect.ValueUsage{
			Path:         "global.proxy.resources.requests.memory",
			DefaultValue: "128Mi",
		},
		Component: "",
	}

	snippet := m.generateBundleSnippetForItem(v)
	for _, want := range []string{
		"- name: <package-name>",
		"      <component-name>:",
		"        istiod:",
		"            - name: <prefix>_GLOBAL_PROXY_RESOURCES_REQUESTS_MEMORY",
		"              default: 128Mi",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("expected snippet to contain %q, got:\n%s", want, snippet)
		}
	}
}
