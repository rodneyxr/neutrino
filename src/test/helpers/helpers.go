package helpers

import (
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
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

// RequireUDSOverrideValues checks that all specified paths exist as UDS override values in the report.
func RequireUDSOverrideValues(t *testing.T, report *introspect.ZarfPackageIntrospection, paths []string) {
	t.Helper()
	for _, expected := range paths {
		found := false
		for _, ov := range report.UDSOverrideValues {
			if ov.Path == expected {
				found = true
				break
			}
		}
		assert.Truef(t, found, "Expected to find UDS override value with path %s", expected)
	}
}

// RequireUDSOverrideVariables checks that all specified variable names exist as UDS override variables in the report.
func RequireUDSOverrideVariables(t *testing.T, report *introspect.ZarfPackageIntrospection, names []string) {
	t.Helper()
	for _, expected := range names {
		found := false
		for _, ov := range report.UDSOverrideVariables {
			if ov.Name == expected {
				found = true
				break
			}
		}
		assert.Truef(t, found, "Expected to find UDS override variable %s", expected)
	}
}

// RequireUDSOverrideValueOnComponent checks that a specific UDS override value exists on a component result.
func RequireUDSOverrideValueOnComponent(t *testing.T, result *introspect.IntrospectResult, path string) {
	t.Helper()
	for _, ov := range result.UDSOverrideValues {
		if ov.Path == path {
			return
		}
	}
	t.Errorf("Expected to find UDS override value with path '%s' on component %s", path, result.ChartName)
}

// RequireUDSOverrideVariableOnComponent checks that a specific UDS override variable exists on a component result.
func RequireUDSOverrideVariableOnComponent(t *testing.T, result *introspect.IntrospectResult, name string) {
	t.Helper()
	for _, ov := range result.UDSOverrideVariables {
		if ov.Name == name {
			return
		}
	}
	t.Errorf("Expected to find UDS override variable '%s' on component %s", name, result.ChartName)
}
