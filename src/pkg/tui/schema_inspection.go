package tui

import "github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"

// Package schema is secondary evidence, including for packages that have no
// supplied settings. Scope can select such a package without inventing a row.
func (m Model) packageSchemaOwner() string {
	owner := ""
	if m.focusPane == FocusComponents {
		if scope, ok := m.workspaceScope(m.scopeTreeKey); ok {
			owner = scope.packageName
		}
	} else if value, ok := m.focusedActionValue(); ok {
		if pkg := m.tracePackage(value); pkg != nil {
			return pkg.PackageName
		}
		owner = m.scopeItemOwner(value)
	}
	if owner == "" && m.focusPane != FocusComponents {
		if scope, ok := m.workspaceScope(m.currentChart); ok {
			owner = scope.packageName
		}
	}
	return owner
}

func (m Model) packageSchemaTarget() *introspect.PackageValues {
	owner := m.packageSchemaOwner()
	for i := range m.report.PackageValues {
		pkg := &m.report.PackageValues[i]
		if owner != "" && pkg.PackageName == owner {
			return pkg
		}
	}
	if owner == "" && len(m.report.PackageValues) == 1 {
		return &m.report.PackageValues[0]
	}
	return nil
}

func (m *Model) openPackageSchema() {
	pkg := m.packageSchemaTarget()
	if pkg == nil {
		if m.packageSchemaOwner() != "" {
			m.setStatus("This package has no inspected schema")
		} else {
			m.setStatus("Select a package in Scope to inspect its schema")
		}
		return
	}
	if len(pkg.Schema) == 0 {
		m.setStatus("This package has no inspected schema")
		return
	}
	value := DisplayItem{ValueUsage: introspect.ValueUsage{Path: "Package schema"},
		Component: "Zarf Package Values", PackageName: pkg.PackageName,
		IsPackageValue: true, ValueSchema: pkg.Schema}
	m.followTrace(DetailItem{Kind: DetailItemTrace, relatedValue: &value})
	m.focusPane, m.showOverview = FocusInspector, false
	m.detailPanel, m.detailSchemaShown = detailSchema, true
	m.statusMessage = ""
	m.buildDetailItems()
	m.ensureDetailCursorVisible()
}
