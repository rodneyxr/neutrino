package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Tab order keeps the existing 1–7 shortcuts; All has shortcut 8. All is
// pinned first visually so a narrow Settings pane always exposes its search
// scope escape hatch. Counts reflect the current scope and query.
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

// A selected tab opens into the content below. Its border shape preserves the
// selected state on terminals without color; zero counts remain explicit.
type settingTab struct {
	shortcut int
	label    string
	count    int
	selected bool
}

func (tab settingTab) text() string {
	return fmt.Sprintf("%d %s %d", tab.shortcut, tab.label, tab.count)
}

func (tab settingTab) render(width, padding int) string {
	inner := max(0, width-2)
	padding = min(padding, max(0, (inner-3)/2))
	room := max(0, inner-padding*2)
	count := fmt.Sprint(tab.count)
	prefix := fmt.Sprintf("%d ", tab.shortcut)
	label := truncateCell(tab.label, max(0, room-len(prefix)-len(count)-1))
	text := prefix + label + " " + count
	if label == "" {
		text = fmt.Sprintf("%d %s", tab.shortcut, count)
	}
	text = truncateCell(text, room)
	text = strings.Repeat(" ", padding) + text + strings.Repeat(" ", padding)
	text += strings.Repeat(" ", max(0, inner-lipgloss.Width(text)))

	border, face, badge := settingsTabBorderStyle, settingsTabStyle, settingsTabCountStyle
	if tab.count == 0 {
		face = settingsTabEmptyStyle
	}
	bottom := "┴" + strings.Repeat("─", inner) + "┴"
	if tab.selected {
		border, face, badge = settingsTabActiveBorderStyle, settingsTabActiveStyle, settingsTabActiveCountStyle
		bottom = "┘" + strings.Repeat(" ", inner) + "└"
	}
	middle := face.Render(text)
	// Style the trailing count independently without splitting an elided label.
	if room >= len(count)+2 {
		at := strings.LastIndex(text, count)
		if at >= 0 {
			middle = face.Render(text[:at]) + badge.Render(count) + face.Render(text[at+len(count):])
		}
	}
	lines := []string{
		border.Render("╭" + strings.Repeat("─", inner) + "╮"),
		border.Render("│") + middle + border.Render("│"),
		border.Render(bottom),
	}
	for i := range lines {
		if width < 2 {
			lines[i] = truncateCell(lines[i], width)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) settingsTabs(width int) string {
	if width <= 0 {
		return ""
	}
	padding := 1
	if width < 60 {
		padding = 0
	}
	var total int
	for _, count := range m.categoryCounts {
		total += count
	}
	all := settingTab{shortcut: 8, label: "All", count: total, selected: m.allCategories}
	tabs := make([]settingTab, len(settingTabLabels))
	for i, label := range settingTabLabels {
		tabs[i] = settingTab{shortcut: i + 1, label: label, count: m.categoryCounts[i], selected: !m.allCategories && i == m.category}
	}
	tabWidth := func(tab settingTab) int { return lipgloss.Width(tab.text()) + 2 + padding*2 }
	allWidth := tabWidth(all)
	selected := clamp(m.category, 0, len(tabs)-1)
	anchor := selected
	if m.allCategories {
		anchor = 0
	}
	// Very small panes prioritize a readable selected tab over a clipped pair.
	if width < allWidth+8 {
		if !m.allCategories {
			return tabs[selected].render(width, 0)
		}
		return all.render(width, padding)
	}
	available := width - allWidth - 1
	start, end, bestCount, bestBalance, bestWidth := anchor, anchor, 0, len(tabs), 0
	for left := 0; left <= anchor; left++ {
		for right := anchor; right < len(tabs); right++ {
			used := right - left // one cell between tabs
			for i := left; i <= right; i++ {
				used += tabWidth(tabs[i])
			}
			if left > 0 {
				used++
			}
			if right < len(tabs)-1 {
				used++
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
				start, end, bestCount, bestBalance, bestWidth = left, right, count, balance, used
			}
		}
	}
	parts := []string{all.render(allWidth, padding), settingsTabGap(1)}
	if bestCount == 0 {
		// Preserve shortcut and exact count when only the selected name needs
		// shortening. All remains visible at supported pane widths.
		before, after := 0, 0
		if anchor > 0 {
			before = 1
		}
		if anchor < len(tabs)-1 {
			after = 1
		}
		if before > 0 {
			parts = append(parts, settingsTabOverflow("‹"))
		}
		parts = append(parts, tabs[anchor].render(max(1, available-before-after), padding))
		if after > 0 {
			parts = append(parts, settingsTabOverflow("›"))
		}
	} else {
		if start > 0 {
			parts = append(parts, settingsTabOverflow("‹"))
		}
		for i := start; i <= end; i++ {
			if i > start {
				parts = append(parts, settingsTabGap(1))
			}
			parts = append(parts, tabs[i].render(tabWidth(tabs[i]), padding))
		}
		if end < len(tabs)-1 {
			parts = append(parts, settingsTabOverflow("›"))
		}
	}
	strip := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	lines := strings.Split(strip, "\n")
	for i, line := range lines {
		remaining := width - lipgloss.Width(line)
		if remaining > 0 {
			if i == 2 {
				lines[i] += settingsTabBorderStyle.Render(strings.Repeat("─", remaining))
			} else {
				lines[i] += strings.Repeat(" ", remaining)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func settingsTabGap(width int) string {
	return strings.Repeat(" ", width) + "\n" + strings.Repeat(" ", width) + "\n" + settingsTabBorderStyle.Render(strings.Repeat("─", width))
}

func settingsTabOverflow(arrow string) string {
	return " \n" + helpStyle.Render(arrow) + "\n" + settingsTabBorderStyle.Render("─")
}
