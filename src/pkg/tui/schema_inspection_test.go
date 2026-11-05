package tui

import (
	"strings"
	"testing"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func schemaOnlyReport() introspect.InspectionReport {
	return introspect.InspectionReport{PackageValues: []introspect.PackageValues{{
		PackageName: "schema-only", Schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"replicas": map[string]interface{}{"type": "integer", "default": 3},
			"password": map[string]interface{}{"type": "string", "default": "PRIVATE_SCHEMA_DEFAULT"},
		}},
	}}}
}

func TestSchemaOnlyPackageRemainsSecondaryAndMasked(t *testing.T) {
	m := NewModel(schemaOnlyReport())
	resizeWorkspace(&m, 120, 32)
	if len(m.allValues) != 0 || len(m.valuePolicy.catalog().packages) == 0 {
		t.Fatal("schema inventory must remain available to policy without creating main settings")
	}
	category := m.category
	pressWorkspaceKey(&m, "S")
	if m.focusPane != FocusInspector || m.detailPanel != detailSchema {
		t.Fatal("package schema must be reachable even without a selectable setting")
	}
	view := m.View()
	if !strings.Contains(view, "replicas") || strings.Contains(view, "PRIVATE_SCHEMA_DEFAULT") || !strings.Contains(view, "annotations") {
		t.Fatalf("secondary schema lost annotations or masking:\n%s", view)
	}
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusValues || m.detailRef != nil || m.category != category || len(m.allValues) != 0 {
		t.Fatal("returning from package schema changed the main catalog")
	}
}

func TestPackageSchemaFromScopeReturnsToSameTreeSelection(t *testing.T) {
	m := NewModel(schemaOnlyReport())
	resizeWorkspace(&m, 160, 32)
	pressWorkspaceKey(&m, "f")
	m.setScopeTreeKey("package:schema-only")
	pressWorkspaceKey(&m, "S")
	if m.focusPane != FocusInspector || m.detailPanel != detailSchema || m.detailRef == nil || m.detailRef.PackageName != "schema-only" {
		t.Fatal("Scope did not open the exact package's schema")
	}
	pressWorkspaceKey(&m, "esc")
	if m.focusPane != FocusComponents || m.scopeTreeKey != "package:schema-only" || m.currentChart != "All Charts" {
		t.Fatal("schema back navigation did not restore Scope without applying it")
	}
}

func TestPackageSchemaDoesNotGuessBetweenPackages(t *testing.T) {
	report := schemaOnlyReport()
	other := report.PackageValues[0]
	other.PackageName = "other"
	report.PackageValues = append(report.PackageValues, other)
	m := NewModel(report)
	m.openPackageSchema()
	if m.focusPane == FocusInspector || m.detailRef != nil || !strings.Contains(m.statusMessage, "Select a package") {
		t.Fatal("ambiguous schema access must request a package selection, not pick a package")
	}
}

func TestPackageSchemaDoesNotCrossAnExplicitPackageBoundary(t *testing.T) {
	report := schemaOnlyReport()
	report.Components = map[string]*introspect.IntrospectResult{
		"other/server/web": {Values: []introspect.ValueUsage{{Path: "enabled", DefaultValue: true}}},
	}
	for _, fromScope := range []bool{true, false} {
		m := NewModel(report)
		resizeWorkspace(&m, 160, 32)
		if fromScope {
			pressWorkspaceKey(&m, "f")
			m.setScopeTreeKey("package:other")
		} else {
			m.selectSettingType(5)
		}
		pressWorkspaceKey(&m, "S")
		if m.detailRef != nil || m.detailPanel == detailSchema || m.statusMessage != "This package has no inspected schema" {
			t.Fatalf("selected other package opened schema-only package's schema: %+v", m.detailRef)
		}
	}
}
