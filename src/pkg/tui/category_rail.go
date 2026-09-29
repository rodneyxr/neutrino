package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Categories keep bundle inputs, package inputs and chart defaults separate.
// Their positions match the single-key shortcuts shown in the rail.
var nextCategories = [...]string{
	"Bundle vars", "Next values files", "Zarf variables", "Zarf Package Values",
	"Chart mappings", "Helm values", "Legacy overrides",
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

func (m *Model) selectNextCategory(index int) {
	m.category = index
	m.filteredValues = m.categoryRows[index]
	m.cursor, m.viewportOffset = 0, 0
	m.pathScroll = 0
	m.pathScrollPause = 2
	m.buildDetailItems()
}

func (m Model) nextCategoryRail(width, height int) string {
	var lines strings.Builder
	lines.WriteString(paneHeadingStyle.Render("CATEGORIES"))
	lines.WriteString("\n\n")
	for i, name := range nextCategories {
		marker := "  "
		if i == m.category {
			marker = "> "
		}
		label := truncateCell(fmt.Sprintf("%s%d %s (%d)", marker, i+1, name, m.categoryCounts[i]), max(1, width-4))
		if i == m.category {
			lines.WriteString(selectedRowStyle.Render(label))
		} else {
			lines.WriteString(helpStyle.Render(label))
		}
		if i+1 < len(nextCategories) {
			lines.WriteByte('\n')
		}
	}
	return paneFrame(width, height, m.focusPane == FocusComponents).Render(lines.String())
}

func (m Model) nextCategoryWorkspace(width, height int) string {
	if width < mediumLayoutWidth {
		railHeight := min(11, max(4, height/2))
		return m.nextCategoryRail(width, railHeight) + "\n" + m.valuesPane(width, max(4, height-railHeight))
	}
	const railWidth = 32
	return lipgloss.JoinHorizontal(lipgloss.Top, m.nextCategoryRail(railWidth, height), m.valuesPane(width-railWidth, height))
}
