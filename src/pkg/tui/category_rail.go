package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// Categories keep bundle inputs, package inputs and chart defaults separate.
// Their positions match the single-key shortcuts shown in the rail.
var nextCategories = [...]string{
	"Bundle vars", "Next values files", "Zarf variables", "Zarf Package Values",
	"Mappings & uses", "Helm values", "Legacy overrides",
}

// Start with the configuration surface owned by the inspected artifact. All
// remains an explicit tab for deliberate cross-type searches.
func (m Model) initialSettingCategory() int {
	order := []int{5, 2, 3, 4, 6, 0, 1}
	if m.nextRail {
		order = []int{0, 1, 3, 2, 5, 4, 6}
	} else if len(m.udsOverrideValues)+len(m.udsOverrideVariables) > 0 {
		order = []int{6, 3, 2, 5, 4, 0, 1}
	} else if len(m.report.PackageValues) > 0 {
		order = []int{3, 2, 4, 5, 6, 0, 1}
	}
	for _, index := range order {
		if m.categoryCounts[index] > 0 {
			return index
		}
	}
	return order[0]
}

func nextCategory(item DisplayItem) int {
	switch {
	case item.IsBundleVariable:
		return 0
	case item.Component == "UDS Next bundle":
		return 1
	case item.IsPkgVar || item.IsZarfVar:
		return 2
	case item.IsPackageMapping:
		return 4
	case item.IsPackageValue:
		return 3
	case item.IsUDSOverrideValue || item.IsUDSOverrideVar:
		return 6
	default:
		return 5
	}
}

// Next inspection prefixes declared variables with "<package ID>." and chart
// identities with "<package ID>/". Match the longest ID to handle dotted IDs.
func (m Model) nextPackageFor(value, separator string) string {
	if !m.nextRail {
		return ""
	}
	owner := ""
	for _, pkg := range m.bundle.Packages {
		if len(pkg.ID) > len(owner) && strings.HasPrefix(value, pkg.ID+separator) {
			owner = pkg.ID
		}
	}
	return owner
}

func (item *DisplayItem) addZarfReference(component string, locations []introspect.ValueUsageLocation) {
	if item.ZarfLocationCount == 0 && len(locations) > 0 {
		item.ZarfFirstLocation = locations[0]
	}
	item.ZarfLocationCount += len(locations)
	item.ZarfReferences = append(item.ZarfReferences,
		zarfVariableReference{component: component, locations: locations})
}

// One package variable can have a declaration and references in several charts.
// Collapse only the Next workspace rows; the inspection report remains intact.
func (m Model) consolidateNextZarfVariables(values []DisplayItem, filter string) []DisplayItem {
	type variableKey struct{ pkg, name string }
	seen := make(map[variableKey]int)
	var declarations map[string]int
	if filter != "" {
		declarations = make(map[string]int, len(m.pkgVariables))
		for i := range m.pkgVariables {
			declarations[m.pkgVariables[i].Name] = i
		}
	}
	grouped := values[:0]
	for _, item := range values {
		if item.PackageName == "" || (!item.IsPkgVar && !item.IsZarfVar) {
			grouped = append(grouped, item)
			continue
		}
		name := item.Path
		if item.IsPkgVar {
			name = strings.TrimPrefix(name, item.PackageName+".")
		}
		key := variableKey{item.PackageName, name}
		if index, ok := seen[key]; ok && item.IsZarfVar {
			grouped[index].addZarfReference(item.Component, item.Locations)
			continue
		}
		if item.IsZarfVar {
			component := item.Component
			if i, ok := declarations[item.PackageName+"."+name]; ok {
				pv := m.pkgVariables[i]
				item.Path, item.DefaultValue, item.PkgVarDesc = pv.Name, pv.Default, pv.Description
				item.IsPkgVar, item.IsZarfVar = true, false
				item.Component = "Zarf Package Variables"
			}
			item.addZarfReference(component, item.Locations)
			item.Locations = nil
			item.Chart = "" // references may span charts; Details identifies each source
		}
		if _, ok := seen[key]; !ok {
			seen[key] = len(grouped)
		}
		grouped = append(grouped, item)
	}
	return grouped
}

func (m *Model) selectNextCategory(index int) {
	m.mapping = nil
	m.mappingRowDetail = false
	if !m.allCategories {
		m.categoryCursors[m.category], m.categoryOffsets[m.category] = m.cursor, m.viewportOffset
	}
	m.showOverview = false
	m.allCategories = false
	m.category = index
	m.filteredValues = m.categoryRows[index]
	m.cursor = clamp(m.categoryCursors[index], 0, len(m.filteredValues)-1)
	m.viewportOffset = clamp(m.categoryOffsets[index], 0, m.cursor)
	m.resetSelectedRowScroll()
	m.syncInspectorSelection()
}

func (m *Model) selectSettingType(index int) {
	if index < len(nextCategories) {
		m.selectNextCategory(index)
	} else {
		if !m.allCategories {
			m.categoryCursors[m.category], m.categoryOffsets[m.category] = m.cursor, m.viewportOffset
		}
		m.mapping, m.mappingRowDetail = nil, false
		m.showOverview, m.allCategories = false, true
		m.filteredValues = m.allValues
		m.cursor, m.viewportOffset = 0, 0
		m.resetSelectedRowScroll()
		m.syncInspectorSelection()
	}
	m.focusPane = FocusValues
	m.ensureCursorVisible()
}

func (m Model) nextCategoryRail(width, height int) string {
	var lines strings.Builder
	lines.WriteString(paneHeadingStyle.Render("CATEGORIES"))
	lines.WriteString("\n")
	lines.WriteString(helpStyle.Render(truncateCell("0 Overview · f Scope", max(1, width-4))))
	lines.WriteString("\n\n")
	for i, name := range nextCategories {
		marker := "  "
		if i == m.category && !m.allCategories {
			marker = "> "
		}
		label := truncateCell(fmt.Sprintf("%s%d %s (%d)", marker, i+1, name, m.categoryCounts[i]), max(1, width-4))
		if i == m.category && !m.allCategories {
			lines.WriteString(selectedRowStyle.Render(label))
		} else {
			lines.WriteString(helpStyle.Render(label))
		}
		if i+1 < len(nextCategories) {
			lines.WriteByte('\n')
		}
	}
	lines.WriteString("\n")
	label := fmt.Sprintf("  8 All inputs (%d)", len(m.allValues))
	if m.allCategories {
		label = ">" + label[1:]
	}
	lines.WriteString(helpStyle.Render(truncateCell(label, max(1, width-4))))
	return paneFrame(width, height, m.focusPane == FocusComponents).Render(lines.String())
}

func (m Model) nextCategoryWorkspace(width, height int) string {
	if width < mediumLayoutWidth && height < 20 {
		if m.focusPane == FocusComponents {
			return m.nextCategoryRail(width, height)
		}
		return m.valuesPane(width, height)
	}
	if width < mediumLayoutWidth {
		railHeight := min(14, max(4, height/2))
		return m.nextCategoryRail(width, railHeight) + "\n" + m.valuesPane(width, max(4, height-railHeight))
	}
	const railWidth = 32
	return lipgloss.JoinHorizontal(lipgloss.Top, m.nextCategoryRail(railWidth, height), m.valuesPane(width-railWidth, height))
}
