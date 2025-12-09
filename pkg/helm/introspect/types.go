package introspect

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

// ValueUsage represents a discovered usage of a value in the templates.
type ValueUsage struct {
	// Path is the dot-notation path to the value (e.g., "image.repository").
	Path string `json:"path"`
	// Chart is the name of the chart where this usage occurs (root or subchart).
	Chart string `json:"chart"`
	// Wildcard indicates if the path contains dynamic segments (e.g., "apps.*.port").
	Wildcard bool `json:"wildcard"`
	// Locations lists all occurrences of this value usage.
	Locations []ValueUsageLocation `json:"locations"`
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
}

// IntrospectOptions configures the introspection process.
type IntrospectOptions struct {
	// IncludeSubcharts determines whether to analyze dependencies.
	IncludeSubcharts bool
	// ExcludeHooks determines whether to skip hook templates.
	ExcludeHooks bool
	// NoCache forces a re-download of the chart, bypassing the cache.
	NoCache bool
}
