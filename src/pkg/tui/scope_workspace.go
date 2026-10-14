package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// Scope identities are independent of display labels and chart path substrings.
type workspaceScope struct {
	key, label, kind string
	depth, charts    int
}

func buildWorkspaceScopes(report introspect.InspectionReport) []workspaceScope {
	scopes := []workspaceScope{{key: "All Charts", label: "All components", kind: "inspection", charts: len(report.Components)}}
	seen := map[string]int{"All Charts": 0}
	add := func(key, label, kind string, depth int) {
		if _, ok := seen[key]; !ok {
			seen[key] = len(scopes)
			scopes = append(scopes, workspaceScope{key: key, label: label, kind: kind, depth: depth})
		}
	}
	if report.Bundle != nil {
		for _, p := range report.Bundle.Packages {
			add("package:"+p.ID, p.ID, "package", 0)
		}
	}
	// Build each package subtree in declaration order, including packages without charts.
	roots := append([]workspaceScope(nil), scopes[1:]...)
	scopes = scopes[:1]
	seen = map[string]int{"All Charts": 0}
	appendChart := func(key string, depth int) {
		parts := strings.Split(key, "/")
		component := ""
		if len(parts) > 1 {
			component = strings.Join(parts[:len(parts)-1], "/")
			add("component:"+component, parts[len(parts)-2], "component", depth)
			depth++
		}
		label := parts[len(parts)-1]
		if chart := report.Components[key]; chart != nil && chart.ChartName != "" {
			label = chart.ChartName
		}
		if label == "" {
			label = "Standalone chart"
		}
		add(key, label, "chart", depth)
	}
	assigned := map[string]bool{}
	for _, root := range roots {
		add(root.key, root.label, root.kind, 0)
		for _, key := range report.ComponentOrder {
			if strings.HasPrefix(key, root.label+"/") {
				appendChart(key, 1)
				assigned[key] = true
				scopes[seen[root.key]].charts++
			}
		}
	}
	for _, key := range report.ComponentOrder {
		if !assigned[key] {
			appendChart(key, 0)
		}
	}
	return scopes
}

func (m Model) chartInScope(key string) bool {
	switch {
	case m.currentChart == "All Charts":
		return true
	case strings.HasPrefix(m.currentChart, "package:"):
		return strings.HasPrefix(key, strings.TrimPrefix(m.currentChart, "package:")+"/")
	case strings.HasPrefix(m.currentChart, "component:"):
		return strings.HasPrefix(key, strings.TrimPrefix(m.currentChart, "component:")+"/")
	default:
		return key == m.currentChart || (m.inputMode == InputModeChartSelect && strings.Contains(strings.ToLower(key), strings.ToLower(m.currentChart)))
	}
}
func (m Model) itemInScope(item DisplayItem) bool {
	if m.currentChart == "All Charts" {
		return true
	}
	if strings.HasPrefix(m.currentChart, "package:") {
		owner := strings.TrimPrefix(m.currentChart, "package:")
		return item.PackageName == owner || strings.HasPrefix(item.Component, owner+"/") || strings.HasPrefix(item.SummaryComponent, owner+"/")
	}
	if item.IsPkgVar && m.nextRail {
		return item.PackageName == m.nextPackageFor(strings.TrimPrefix(m.currentChart, "component:"), "/")
	}
	return !item.IsBundleVariable
}
func (m Model) scopeLabel() string {
	for _, scope := range m.scopes {
		if scope.key == m.currentChart {
			if scope.kind == "inspection" && m.bundle != nil {
				name := m.bundle.Name
				if name == "" {
					name = "Bundle"
				}
				return name + " / all packages"
			}
			label := strings.TrimPrefix(strings.TrimPrefix(scope.key, "package:"), "component:")
			if label == "" {
				label = scope.label
			}
			return scope.kind + " / " + label
		}
	}
	return m.componentLabel(m.currentChart)
}
func (m Model) matchingScopes() []workspaceScope {
	query := strings.ToLower(strings.TrimSpace(m.chartInput.Value()))
	var out []workspaceScope
	for _, scope := range m.scopes {
		if query == "" || strings.Contains(strings.ToLower(scope.label+" "+scope.key+" "+scope.kind), query) {
			out = append(out, scope)
		}
	}
	return out
}
func (m *Model) beginScopePicker() {
	m.inputMode = InputModeScopeSelect
	m.chartInput.SetValue("")
	m.chartInput.Focus()
	m.scopeCursor = 0
	for i, s := range m.scopes {
		if s.key == m.currentChart {
			m.scopeCursor = i
			break
		}
	}
	m.statusMessage = ""
}
func (m Model) handleScopeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.matchingScopes()
	switch msg.String() {
	case "esc":
		m.inputMode = InputModeNormal
		m.chartInput.Blur()
	case "up", "ctrl+k":
		m.scopeCursor = clamp(m.scopeCursor-1, 0, len(choices)-1)
	case "down", "ctrl+j":
		m.scopeCursor = clamp(m.scopeCursor+1, 0, len(choices)-1)
	case "pgup":
		m.scopeCursor = clamp(m.scopeCursor-max(1, m.windowHeight-9), 0, len(choices)-1)
	case "pgdown":
		m.scopeCursor = clamp(m.scopeCursor+max(1, m.windowHeight-9), 0, len(choices)-1)
	case "enter":
		if len(choices) > 0 {
			m.currentChart = choices[clamp(m.scopeCursor, 0, len(choices)-1)].key
			m.inputMode = InputModeNormal
			m.chartInput.Blur()
			m.showOverview = false
			m.updateFilteredValues()
			m.cursor, m.viewportOffset = 0, 0
			m.focusPane = FocusValues
			m.buildDetailItems()
			m.setStatus("Scope: " + m.scopeLabel())
		}
	default:
		var cmd tea.Cmd
		m.chartInput, cmd = m.chartInput.Update(msg)
		m.scopeCursor = 0
		return m, cmd
	}
	return m, nil
}
func (m Model) scopePicker(width, height int) string {
	inner := max(1, width-4)
	var lines []string
	lines = append(lines, paneHeadingStyle.Render("SCOPE · package / component / chart"), truncateCell(m.chartInput.View(), inner), "")
	choices := m.matchingScopes()
	visible := max(1, height-7)
	start := max(0, m.scopeCursor-visible+1)
	for i := start; i < len(choices) && i < start+visible; i++ {
		s := choices[i]
		label := strings.Repeat("  ", s.depth) + s.label + "  · " + s.kind
		marker := "  "
		if s.key == m.currentChart {
			label += " ✓"
		}
		if i == m.scopeCursor {
			marker = "> "
		}
		line := truncateCell(marker+label, inner)
		if i == m.scopeCursor {
			line = selectedRowStyle.Render(line)
		}
		lines = append(lines, line)
	}
	if len(choices) == 0 {
		lines = append(lines, "No matching scopes")
	}
	lines = append(lines, "", helpStyle.Render(truncateCell("↑↓ choose  type search  enter scope  esc back", inner)))
	return paneFrame(width, height, true).Render(strings.Join(lines, "\n"))
}

// Structured queries are AND-combined; bare terms retain substring search.
func (m Model) matchesWorkspaceFilter(item DisplayItem) bool {
	query := strings.TrimSpace(m.filterInput.Value())
	if query == "" {
		return true
	}
	if item.IsEmptyState {
		return false
	}
	for _, term := range strings.Fields(query) {
		key, value, qualified := strings.Cut(strings.ToLower(term), ":")
		matched := false
		if qualified {
			switch key {
			case "package", "pkg":
				owner := item.PackageName
				if owner == "" {
					owner = m.nextPackageFor(item.Component, "/")
				}
				matched = strings.Contains(strings.ToLower(owner), value)
			case "chart":
				charts := item.Component + " " + item.Chart + " " + item.MappingChart
				for _, ref := range item.ZarfReferences {
					charts += " " + ref.component
				}
				matched = strings.Contains(strings.ToLower(charts), value)
			case "source":
				matched = strings.Contains(strings.ToLower(m.itemSources(item)), value)
			case "kind":
				matched = strings.Contains(strings.ToLower(nextCategories[nextCategory(item)]), value)
			case "status":
				switch value {
				case "changed", "overridden":
					matched = m.valueChanged(item)
				case "runtime", "unresolved":
					matched = m.valueUnresolved(item)
				case "excluded", "unmapped", "mapped", "mixed":
					matched = item.MappingStatus == value
				default:
					matched = false
				}
			default:
				matched = matchesValueFilter(item, term)
			}
		} else {
			matched = matchesValueFilter(item, term)
		}
		if !matched {
			return false
		}
	}
	return true
}

// Keep declaration metadata while narrowing references to the charts that matched.
func (m Model) filteredReferences(item DisplayItem) DisplayItem {
	if len(item.ZarfReferences) == 0 || m.filterInput.Value() == "" {
		return item
	}
	references := item.ZarfReferences
	base := item
	base.ZarfReferences = nil
	if m.matchesWorkspaceFilter(base) {
		return item
	}
	item.ZarfReferences = nil
	item.ZarfLocationCount = 0
	item.ZarfFirstLocation = introspect.ValueUsageLocation{}
	for _, ref := range references {
		candidate := base
		candidate.ZarfReferences = []zarfVariableReference{ref}
		if m.matchesWorkspaceFilter(candidate) {
			item.addZarfReference(ref.component, ref.locations)
		}
	}
	return item
}

func (m Model) itemSources(item DisplayItem) string {
	parts := append([]string{item.ValueSource}, item.ValueFiles...)
	for _, location := range item.Locations {
		parts = append(parts, location.File)
	}
	for _, ref := range item.ZarfReferences {
		for _, location := range ref.locations {
			parts = append(parts, location.File)
		}
	}
	if chart := m.components[item.Component]; chart != nil {
		parts = append(parts, chart.Source)
		parts = append(parts, chart.ValuesFiles...)
	}
	if item.IsBundleVariable && m.bundle != nil {
		for _, source := range m.bundle.VariableSources {
			parts = append(parts, source.Name)
		}
	}
	if pkg := m.tracePackage(item); pkg != nil {
		for _, source := range pkg.Sources {
			parts = append(parts, source.Name)
		}
	}
	return strings.Join(parts, " ")
}

func (m *Model) focusStatus(status string) {
	query := "status:" + status
	if m.filterInput.Value() == query {
		query = ""
	}
	m.filterInput.SetValue(query)
	m.allCategories = true
	m.showOverview = false
	m.focusPane = FocusValues
	m.updateFilteredValues()
	m.cursor, m.viewportOffset = 0, 0
	m.buildDetailItems()
}
func (m Model) overviewPane(width, height int) string {
	inner := max(1, width-4)
	var lines []string
	add := func(s string) { lines = append(lines, truncateCell(s, inner)) }
	add(paneHeadingStyle.Render("INSPECTION OVERVIEW"))
	add(m.scopeLabel())
	add("")
	packages := len(m.report.PackageValues)
	if m.bundle != nil {
		packages = len(m.bundle.Packages)
	}
	chartCount := 0
	for key := range m.components {
		if m.chartInScope(key) {
			chartCount++
		}
	}
	if strings.HasPrefix(m.currentChart, "package:") || strings.HasPrefix(m.currentChart, "component:") || m.currentChart != "All Charts" && packages > 0 {
		packages = 1
	}
	add(fmt.Sprintf("%d packages · %d charts", packages, chartCount))
	if m.report.PackageAPIVersion != "" {
		add("Package API: " + m.report.PackageAPIVersion)
	}
	changed, unresolved := 0, 0
	for _, item := range m.allValues {
		if m.valueChanged(item) {
			changed++
		}
		if m.valueUnresolved(item) {
			unresolved++
		}
	}
	add(fmt.Sprintf("c  Changed items        %d", changed))
	add(fmt.Sprintf("u  Unresolved items     %d", unresolved))
	add("8  Browse all inputs")
	add("f  Choose package / chart")
	add("")
	add("CONFIGURATION FLOW")
	if m.bundle != nil && m.bundle.Format == "next" {
		add("Bundle .vars → values files")
	}
	if len(m.report.PackageValues) > 0 {
		add("Zarf Package .Values → chart mappings")
	}
	if m.bundle != nil && m.bundle.Format == "legacy" {
		add("Legacy UDS overrides → Helm .Values")
	}
	if m.bundle == nil && len(m.report.PackageValues) == 0 {
		add("Chart defaults → Helm .Values")
	}
	add("Helm .Values → template locations")
	add("")
	add("Static evidence; runtime inputs stay unresolved.")
	if m.bundle != nil && height > 23 {
		add("")
		add("PACKAGES · dependencies order deployment only")
		for _, pkg := range m.bundle.Packages {
			label := pkg.ID
			if pkg.APIVersion != "" {
				label += " · " + pkg.APIVersion
			}
			if len(pkg.DependsOn) > 0 {
				label += "  after " + strings.Join(pkg.DependsOn, ", ")
			}
			add(label)
		}
	}
	return paneFrame(width, height, m.focusPane == FocusValues).Render(clipLinesAt(strings.Join(lines, "\n"), max(1, height-2), m.overviewOffset))
}

// Sanitize untrusted terminal text at the data boundary; keep only printable text.
func terminalText(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if r < 32 || (r >= 127 && r <= 159) {
			return ' '
		}
		return r
	}, value)
}
func terminalMultiline(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r < 32 || (r >= 127 && r <= 159) {
			return ' '
		}
		return r
	}, value)
}
func fitWorkspace(value string, width, height int) string {
	if width < 1 || height < 1 {
		return value
	}
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
		if padding := width - lipgloss.Width(lines[i]); padding > 0 {
			lines[i] += strings.Repeat(" ", padding)
		}
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}
