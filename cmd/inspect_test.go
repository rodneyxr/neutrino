package cmd

import (
	"context"
	"testing"

	"github.com/rodneyxr/neutrino/pkg/helm/introspect"

	"github.com/stretchr/testify/assert"
)

func Test_isZarfPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		want      bool
		wantError bool
		skip      bool // Skip tests that require network if needed
	}{
		{
			name:   "Zarf tarball",
			source: "zarf-package-test.tar.zst",
			want:   true,
		},
		{
			name:   "Non-Zarf OCI reference (regression test)",
			source: "oci://ghcr.io/stefanprodan/charts/podinfo",
			want:   false,
		},
		{
			name:   "Invalid OCI reference (connection failure)",
			source: "oci://localhost:12345/does-not-exist",
			want:   false,
		},
		{
			name:   "Regular file path",
			source: "./Chart.yaml",
			want:   false,
		},
		{
			name:   "Remote URL (http)",
			source: "https://example.com/chart",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip("Skipping test case")
			}

			// We can pass empty arch as it defaults in the function
			got, err := introspect.IsZarfPackage(context.Background(), tt.source, "")

			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_isRemote tests the helper function
func Test_isRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source string
		want   bool
	}{
		{"oci://example.com", true},
		{"http://example.com", true},
		{"https://example.com", true},
		{"./local/path", false},
		{"/absolute/path", false},
		{"short", false},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := introspect.IsRemote(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Mocking context/writer for introspect command would require more refactoring,
// so limiting to these helper unit tests for now.
