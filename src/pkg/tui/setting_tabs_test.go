package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingsTabsFitEveryTypeAtPaneWidths(t *testing.T) {
	counts := [7]int{0, 12, 0, 32, 6, 1550, 0}
	for _, width := range []int{32, 36, 42, 62, 84, 160} {
		for category := 0; category <= len(settingTabLabels); category++ {
			t.Run(fmt.Sprintf("%d/type%d", width, category), func(t *testing.T) {
				m := Model{category: category % len(settingTabLabels), allCategories: category == len(settingTabLabels), categoryCounts: counts}
				strip := m.settingsTabs(width)
				lines := strings.Split(ansi.Strip(strip), "\n")
				if lipgloss.Width(strip) > width || len(lines) != 3 {
					t.Fatalf("tabs must fit three %d-cell lines: %q", width, lines)
				}
				if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[1], "8 All 1600") {
					t.Fatalf("bordered All tab and total must remain visible: %q", lines)
				}
				if strings.Count(lines[2], "┘") != 1 || strings.Count(lines[2], "└") != 1 {
					t.Fatalf("one tab must open into its content without relying on color: %q", lines[2])
				}
				if !m.allCategories {
					start := strings.Index(lines[2], "┘")
					end := strings.Index(lines[2], "└")
					// Borders are multibyte: terminal cell offsets identify the tab.
					start = lipgloss.Width(lines[2][:start])
					end = lipgloss.Width(lines[2][:end])
					middle := []rune(lines[1])[start+1 : end]
					text := strings.TrimSpace(string(middle))
					if !strings.HasPrefix(text, fmt.Sprintf("%d ", m.category+1)) || !strings.HasSuffix(text, fmt.Sprintf(" %d", counts[m.category])) {
						t.Fatalf("selected shortcut and exact count lost: %q", text)
					}
				}
			})
		}
	}
}

func TestSettingsTabsExposeNeighborsAndOverflow(t *testing.T) {
	m := Model{category: 3}
	narrow := ansi.Strip(m.settingsTabs(32))
	wide := ansi.Strip(m.settingsTabs(84))
	if !strings.Contains(narrow, "‹") || !strings.Contains(narrow, "›") || !strings.Contains(narrow, "4 Zarf values 0") {
		t.Fatalf("narrow tabs must keep selected type and advertise overflow: %s", narrow)
	}
	if !strings.Contains(wide, "3 Zarf vars 0") || !strings.Contains(wide, "5 Mappings 0") {
		t.Fatalf("wider tabs must expose neighboring types with counts: %s", wide)
	}
	first := ansi.Strip((Model{category: 0}).settingsTabs(32))
	last := ansi.Strip((Model{category: 6}).settingsTabs(32))
	if strings.Contains(first, "‹") || !strings.Contains(first, "›") || !strings.Contains(last, "‹") || strings.Contains(last, "›") {
		t.Fatalf("overflow direction must match hidden neighbors: first=%s last=%s", first, last)
	}
}

func TestSettingsTabsWideShowsEveryCountIncludingEmptyTypes(t *testing.T) {
	counts := [7]int{1, 12, 0, 32, 6, 1550, 0}
	for _, all := range []bool{false, true} {
		m := Model{category: 3, allCategories: all, categoryCounts: counts}
		plain := ansi.Strip(m.settingsTabs(240))
		if strings.ContainsAny(plain, "‹›") {
			t.Fatalf("no overflow expected: %s", plain)
		}
		for i, label := range settingTabLabels {
			if text := fmt.Sprintf("%d %s %d", i+1, label, counts[i]); strings.Count(plain, text) != 1 {
				t.Fatalf("missing exact type/count %q: %s", text, plain)
			}
		}
		if !strings.Contains(plain, "8 All 1601") {
			t.Fatalf("wrong total: %s", plain)
		}
	}
}

func TestSettingsTabsTinyWidthsUseTerminalCells(t *testing.T) {
	for _, all := range []bool{false, true} {
		m := Model{category: 3, allCategories: all, categoryCounts: [7]int{1000000, 12, 0, 32, 6, 1550, 0}}
		for width := -1; width <= 40; width++ {
			strip := m.settingsTabs(width)
			if lipgloss.Width(strip) > max(0, width) || strings.ContainsAny(ansi.Strip(strip), "\r\t") {
				t.Fatalf("width%d,All%v: %q", width, all, strip)
			}
			if width > 0 && lipgloss.Height(strip) != 3 {
				t.Fatalf("height changed at width %d: %q", width, strip)
			}
		}
	}
}

func TestSettingsTabCountsFollowScopeAndQuery(t *testing.T) {
	m := settingsTypesModel()
	m.currentChart = "package:api"
	m.updateFilteredValues()
	m.selectSettingType(7)
	m.filterInput.SetValue("replicas")
	m.updateFilteredValues()
	plain := ansi.Strip(m.settingsTabs(240))
	for _, want := range []string{"8 All 2", "1 Bundle vars 0", "4 Zarf values 1", "6 Helm 1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("scoped query must determine tab counts, missing %q: %s", want, plain)
		}
	}
	m.filterInput.SetValue("missing")
	m.updateFilteredValues()
	plain = ansi.Strip(m.settingsTabs(240))
	if !strings.Contains(plain, "8 All 0") || !strings.Contains(plain, "4 Zarf values 0") {
		t.Fatalf("query miss must zero counts: %s", plain)
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
