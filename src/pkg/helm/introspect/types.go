package introspect

import (
	"encoding/json"
	"fmt"

	"github.com/zarf-dev/zarf/src/api/v1alpha1"
)

// ChartSource defines where to load the chart from.
type ChartSource struct {
	// LocalPath is the filesystem path to the chart directory or archive.
	LocalPath string
	// URL is the remote URL (OCI or HTTP) for the chart.
	URL string
	// Version is the version to fetch if URL is used.
	Version string
	// RepoName is the repository name for classic Helm repos.
	RepoName string
	// ValuesFiles contains custom values files to load (e.g. from a zarf package)
	ValuesFiles []string
}

// ValueUsageLocation describes where a value usage was found in a template.
type ValueUsageLocation struct {
	// File is the template file path relative to the chart root.
	File string `json:"file"`
	// Line is the line number where the usage starts (1-based).
	Line int `json:"line"`
	// Column is the column number where the usage starts (1-based), if available.
	Column int `json:"column"`
	// Kind describes the context of usage (e.g., "field", "range", "with", "index").
	Kind string `json:"kind"`
}

func (l ValueUsageLocation) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Kind     string `json:"kind"`
		Location string `json:"location"`
	}
	return json.Marshal(&Alias{
		Kind:     l.Kind,
		Location: fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column),
	})
}

func (l ValueUsageLocation) MarshalYAML() (interface{}, error) {
	type Alias struct {
		Kind     string `yaml:"kind"`
		Location string `yaml:"location"`
	}
	return &Alias{
		Kind:     l.Kind,
		Location: fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Column),
	}, nil
}

// ValueUsage represents a discovered usage of a value in the templates.
type ValueUsage struct {
	// Path is the dot-notation path to the value (e.g., "image.repository").
	Path string `json:"path"`
	// Chart is the name of the chart where this usage occurs (root or subchart).
	Chart string `json:"chart"`
	// Wildcard indicates if the path contains dynamic segments (e.g., "apps.*.port").
	Wildcard bool `json:"-" yaml:"-"`
	// Locations lists all occurrences of this value usage.
	Locations []ValueUsageLocation `json:"locations"`
	// DefaultValue is the value found in the chart's values.yaml at this path.
	DefaultValue interface{} `json:"default,omitempty" yaml:"default,omitempty"`
}

// ZarfVariableUsage represents a discovered usage of a Zarf variable.
type ZarfVariableUsage struct {
	// Name is the name of the Zarf variable (without ###ZARF_VAR_...###).
	Name string `json:"name"`
	// DefaultValue is the default value of the variable from zarf.yaml.
	DefaultValue interface{} `json:"default,omitempty" yaml:"default,omitempty"`
	// Locations lists all occurrences of this variable usage.
	Locations []ValueUsageLocation `json:"locations"`
}

// UDSOverrideValue represents a hard-coded value override from a uds-bundle.yaml.
// These are set directly and are NOT overridable via uds-config.yaml.
type UDSOverrideValue struct {
	// Path is the Helm values path being overridden (e.g., "tls.supportTLSV1_2").
	Path string `json:"path"`
	// Value is the value being set.
	Value interface{} `json:"value"`
	// OriginalDefault is the original default from the chart's values.yaml, if found.
	OriginalDefault interface{} `json:"originalDefault,omitempty" yaml:"originalDefault,omitempty"`
}

// UDSOverrideVariable represents a variable exposed via a uds-bundle.yaml that can be
// overridden by the user via uds-config.yaml.
type UDSOverrideVariable struct {
	// Name is the variable name (e.g., "ISTIO_REQUESTS_MEMORY").
	Name string `json:"name"`
	// Description is the human-readable description of the variable.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Path is the Helm values path this variable maps to (e.g., "global.proxy.resources.requests.memory").
	Path string `json:"path"`
	// Default is the default value for this variable if set in the bundle.
	Default interface{} `json:"default,omitempty" yaml:"default,omitempty"`
	// OriginalDefault is the original default from the chart's values.yaml, if found.
	OriginalDefault interface{} `json:"originalDefault,omitempty" yaml:"originalDefault,omitempty"`
}

// IntrospectResult contains the full analysis result for a chart.
type IntrospectResult struct {
	// ChartName is the name of the root chart.
	ChartName string `json:"chartName"`
	// ChartVersion is the version of the root chart.
	ChartVersion string `json:"chartVersion"`
	// Source describes where the chart was loaded from.
	Source string `json:"source"`
	// Values is the list of discovered overridable values.
	Values []ValueUsage `json:"values"`
	// ValuesFiles is the list of values files used (e.g. values.yaml).
	ValuesFiles []string `json:"valuesFiles"`
	// RemoteSource is the URL to the chart source code (e.g. GitHub repo), if found.
	RemoteSource string `json:"remoteSource,omitempty"`
	// ZarfVars is the list of discovered Zarf variables.
	ZarfVars []ZarfVariableUsage `json:"zarfVars,omitempty"`
	// UDSOverrideValues contains hard-coded value overrides from uds-bundle.yaml.
	UDSOverrideValues []UDSOverrideValue `json:"udsOverrideValues,omitempty" yaml:"udsOverrideValues,omitempty"`
	// UDSOverrideVariables contains variables exposed via uds-bundle.yaml overrides.
	UDSOverrideVariables []UDSOverrideVariable `json:"udsOverrideVariables,omitempty" yaml:"udsOverrideVariables,omitempty"`
}

// IntrospectOptions configures the introspection process.
type IntrospectOptions struct {
	// IncludeSubcharts determines whether to analyze dependencies.
	IncludeSubcharts bool
	// ExcludeHooks determines whether to skip hook templates.
	ExcludeHooks bool
	// NoCache forces a re-download of the chart, bypassing the cache.
	NoCache bool
	// NoValues disables the inference of nested paths from values.yaml.
	NoValues bool
}

// UDSOverrideContext wraps an override value or variable with its package/component/chart context.
type UDSOverrideContext struct {
	PackageName   string `json:"packageName" yaml:"packageName"`
	ComponentName string `json:"componentName" yaml:"componentName"`
	ChartName     string `json:"chartName" yaml:"chartName"`
}

// UDSOverrideValueWithContext pairs an override value with its source context.
type UDSOverrideValueWithContext struct {
	UDSOverrideContext `json:",inline" yaml:",inline"`
	UDSOverrideValue   `json:",inline" yaml:",inline"`
}

// UDSOverrideVariableWithContext pairs an override variable with its source context.
type UDSOverrideVariableWithContext struct {
	UDSOverrideContext  `json:",inline" yaml:",inline"`
	UDSOverrideVariable `json:",inline" yaml:",inline"`
}

// InspectionReport is the unified output structure for purely Helm, Zarf, and UDS introspection.
type InspectionReport struct {
	Variables            []v1alpha1.InteractiveVariable   `json:"variables,omitempty" yaml:"variables,omitempty"`
	Components           map[string]*IntrospectResult     `json:"components" yaml:"components"`
	ComponentOrder       []string                         `json:"-" yaml:"-"`
	UDSOverrideValues    []UDSOverrideValueWithContext    `json:"udsOverrideValues,omitempty" yaml:"udsOverrideValues,omitempty"`
	UDSOverrideVariables []UDSOverrideVariableWithContext `json:"udsOverrideVariables,omitempty" yaml:"udsOverrideVariables,omitempty"`
}
