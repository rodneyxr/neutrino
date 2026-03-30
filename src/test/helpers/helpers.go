package helpers

import (
	"testing"

	"github.com/rodneyxr/neutrino/src/pkg/helm/introspect"
	"github.com/stretchr/testify/assert"
)

// RequireComponentExists checks if any of the given component names exist in the Zarf report.
// Returns the first matched IntrospectResult.
func RequireComponentExists(t *testing.T, report *introspect.ZarfPackageIntrospection, names ...string) *introspect.IntrospectResult {
	t.Helper()
	for _, name := range names {
		if res, ok := report.ChartResults[name]; ok {
			return res
		}
	}
	t.Fatalf("Expected to find one of the following components in the package: %v", names)
	return nil
}

// RequireChartValues is a simplified wrapper to check expected paths on a result.
func RequireChartValues(t *testing.T, result *introspect.IntrospectResult, paths []string) {
	t.Helper()
	RequirePathsExist(t, result, paths)
}

// RequirePathsExist asserts that all provided dot-notation paths were parsed and linked successfully.
func RequirePathsExist(t *testing.T, result *introspect.IntrospectResult, paths []string) {
	t.Helper()
	for _, p := range paths {
		found := false
		for _, v := range result.Values {
			if v.Path == p {
				found = true
				break
			}
		}
		assert.Truef(t, found, "Expected to find value path '%s' in %s chart", p, result.ChartName)
	}
}

// RequireZarfVarsExist checks if the exact Zarf Variables are pulled globally into the report.
func RequireZarfVarsExist(t *testing.T, report *introspect.ZarfPackageIntrospection, vars []string) {
	t.Helper()
	for _, expected := range vars {
		found := false
		for _, v := range report.Variables {
			if v.Name == expected {
				found = true
				break
			}
		}
		assert.Truef(t, found, "Expected to find Zarf variable %s", expected)
	}
}
