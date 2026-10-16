package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingsTabsFitEveryTypeAtPaneWidths(t *testing.T) {
	for _, width := range []int{32, 36, 42, 62, 84} {
		for category := 0; category <= len(settingTabLabels); category++ {
			t.Run(fmt.Sprintf("%d/type%d", width, category), func(t *testing.T) {
				m := Model{category: category % len(settingTabLabels), allCategories: category == len(settingTabLabels)}
				line := m.settingsTabs(width)
				plain := ansi.Strip(line)
				if lipgloss.Width(line) > width || strings.ContainsAny(plain, "\n\r\t") {
					t.Fatalf("tabs exceed one %d-cell line: %q (%d cells)", width, plain, lipgloss.Width(line))
				}
				if !strings.HasPrefix(plain, "8 All │ ") && !strings.HasPrefix(plain, "[8 All] │ ") {
					t.Fatalf("All must remain pinned first: %q", plain)
				}
				selected := "[8 All]"
				if !m.allCategories {
					selected = fmt.Sprintf("[%d %s]", m.category+1, settingTabLabels[m.category])
				}
				if strings.Count(plain, selected) != 1 || strings.Count(plain, "[") != 1 || strings.Count(plain, "]") != 1 {
					t.Fatalf("selected type must be unambiguous without color: want %q in %q", selected, plain)
				}
			})
		}
	}
}

func TestSettingsTabsExposeNeighborsAndOverflow(t *testing.T) {
	m := Model{category: 3}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{32, "8 All │ ← [4 Zarf values] →"},
		{36, "8 All │ ← [4 Zarf values] →"},
		{42, "8 All │ ← 3 Zarf vars [4 Zarf values] →"},
		{62, "8 All │ ← 3 Zarf vars [4 Zarf values] 5 Mappings 6 Helm →"},
		{84, "8 All │ 1 Bundle vars 2 Bundle files 3 Zarf vars [4 Zarf values] 5 Mappings 6 Helm →"},
	} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			if got := ansi.Strip(m.settingsTabs(tc.width)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	first := ansi.Strip((Model{category: 0}).settingsTabs(32))
	last := ansi.Strip((Model{category: 6}).settingsTabs(32))
	if strings.Contains(first, "←") || !strings.Contains(first, "→") {
		t.Fatalf("first type should advertise only later hidden types: %q", first)
	}
	if !strings.Contains(last, "←") || strings.Contains(last, "→") {
		t.Fatalf("last type should advertise only earlier hidden types: %q", last)
	}
}

func TestSettingsTabsWideShowsEveryStableShortcut(t *testing.T) {
	for _, all := range []bool{false, true} {
		m := Model{category: 3, allCategories: all}
		plain := ansi.Strip(m.settingsTabs(160))
		if strings.ContainsAny(plain, "←→") {
			t.Fatalf("all types fit; no overflow arrows expected: %q", plain)
		}
		for i, label := range settingTabLabels {
			if strings.Count(plain, fmt.Sprintf("%d %s", i+1, label)) != 1 {
				t.Fatalf("shortcut %d must appear exactly once, even with no rows: %q", i+1, plain)
			}
		}
	}
}

func TestSettingsTabsTinyWidthsUseTerminalCells(t *testing.T) {
	for _, all := range []bool{false, true} {
		m := Model{category: 3, allCategories: all}
		for width := -1; width <= 31; width++ {
			line := m.settingsTabs(width)
			if lipgloss.Width(line) > max(0, width) || strings.ContainsAny(ansi.Strip(line), "\n\r") {
				t.Fatalf("width %d, all %v: %q occupies %d cells", width, all, line, lipgloss.Width(line))
			}
			if width >= 7 && !strings.Contains(ansi.Strip(line), "8 All") {
				t.Fatalf("All shortcut lost at width %d: %q", width, line)
			}
		}
	}
}

func TestAdjacentSettingTypeWrapsThroughAllAndEmptyTypes(t *testing.T) {
	for current := 0; current < 8; current++ {
		// Counts are deliberately all zero: empty types retain their position.
		m := Model{category: current % 7, allCategories: current == 7}
		for _, delta := range []int{-17, -8, -1, 0, 1, 8, 17} {
			want := ((current+delta)%8 + 8) % 8
			if got := m.adjacentSettingType(delta); got != want {
				t.Errorf("from %d, delta %d: got %d, want %d", current, delta, got, want)
			}
		}
	}
}

func TestSelectedSettingTypeLabelUsesFullNames(t *testing.T) {
	for i, want := range nextCategories {
		if got := (Model{category: i}).selectedSettingTypeLabel(); got != want {
			t.Errorf("category %d: got %q, want %q", i, got, want)
		}
	}
	if got := (Model{category: 3, allCategories: true}).selectedSettingTypeLabel(); got != "All settings" {
		t.Errorf("All label = %q", got)
	}
}
