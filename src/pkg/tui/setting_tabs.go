package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Tab order keeps the existing 1–7 shortcuts; All has shortcut 8. All is
// pinned first visually so a narrow Settings pane always exposes its search
// scope escape hatch. Counts belong in the pane heading, not the tab labels.
var settingTabLabels = [...]string{
	"Bundle vars", "Bundle files", "Zarf vars", "Zarf values",
	"Mappings", "Helm", "Overrides",
}

func (m Model) selectedSettingTypeLabel() string {
	if m.allCategories {
		return "All settings"
	}
	return nextCategories[clamp(m.category, 0, len(nextCategories)-1)]
}

// adjacentSettingType returns a category index, or 7 for All. Empty types
// remain in the cycle: changing scope or search must not change the shortcuts
// or the order of navigation. The caller owns selection and cursor restoration.
func (m Model) adjacentSettingType(delta int) int {
	count := len(settingTabLabels) + 1
	current := clamp(m.category, 0, count-2)
	if m.allCategories {
		current = count - 1
	}
	return (current + delta%count + count) % count
}

func (m Model) settingsTabs(width int) string {
	if width <= 0 {
		return ""
	}
	all := "8 All"
	if m.allCategories {
		all = "[" + all + "]"
	}
	allWidth := lipgloss.Width(all)
	if width < allWidth {
		return selectedRowStyle.Render(truncateCell(all, width))
	}
	allTab := helpStyle.Render(all)
	if m.allCategories {
		allTab = selectedRowStyle.Render(all)
	}
	const divider = " │ "
	available := width - allWidth - lipgloss.Width(divider)
	if available < 3 {
		return allTab
	}

	selected := clamp(m.category, 0, len(settingTabLabels)-1)
	anchor := selected
	if m.allCategories {
		anchor = 0
	}
	labels := make([]string, len(settingTabLabels))
	for i, label := range settingTabLabels {
		labels[i] = fmt.Sprintf("%d %s", i+1, label)
		if !m.allCategories && i == selected {
			labels[i] = "[" + labels[i] + "]"
		}
	}

	// Seven types make an exhaustive window search inexpensive. Keep the
	// selected type visible, fit the most complete neighboring tabs, then prefer
	// a balanced window. Overflow arrows take real terminal cells in this budget.
	start, end, bestCount, bestBalance, bestWidth := anchor, anchor, 0, len(labels), 0
	for left := 0; left <= anchor; left++ {
		for right := anchor; right < len(labels); right++ {
			used := lipgloss.Width(strings.Join(labels[left:right+1], " "))
			if left > 0 {
				used += 2 // "← "
			}
			if right < len(labels)-1 {
				used += 2 // " →"
			}
			if used > available {
				continue
			}
			count := right - left + 1
			balance := (anchor - left) - (right - anchor)
			if balance < 0 {
				balance = -balance
			}
			if count > bestCount || count == bestCount && (balance < bestBalance || balance == bestBalance && used > bestWidth) {
				start, end = left, right
				bestCount, bestBalance, bestWidth = count, balance, used
			}
		}
	}
	if bestCount == 0 {
		// Below normal pane widths, keep All intact and bracket the shortened
		// selected type. Truncate before styling so ANSI color remains intact.
		label := truncateCell(strings.Trim(labels[anchor], "[]"), available-2)
		if !m.allCategories {
			return allTab + helpStyle.Render(divider) + selectedRowStyle.Render("["+label+"]")
		}
		return allTab + helpStyle.Render(divider+truncateCell(labels[anchor], available))
	}
	var tabs strings.Builder
	tabs.WriteString(allTab)
	tabs.WriteString(helpStyle.Render(divider))
	if start > 0 {
		tabs.WriteString(helpStyle.Render("← "))
	}
	for i := start; i <= end; i++ {
		if i > start {
			tabs.WriteByte(' ')
		}
		if !m.allCategories && i == selected {
			tabs.WriteString(selectedRowStyle.Render(labels[i]))
		} else {
			tabs.WriteString(helpStyle.Render(labels[i]))
		}
	}
	if end < len(labels)-1 {
		tabs.WriteString(helpStyle.Render(" →"))
	}
	return tabs.String()
}
