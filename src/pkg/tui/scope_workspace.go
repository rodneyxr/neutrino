package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// Scope identities are independent of display labels and chart path substrings.
type workspaceScope struct {
	key, label, kind       string
	parentKey, packageName string
	depth, charts          int
	chartKeys              []string
}

// Preserve declared chart order, then include map-only snapshots deterministically.
// The empty key is the real identity of a standalone Helm chart.
func orderedScopeCharts(report introspect.InspectionReport) []string {
	seen := make(map[string]bool, len(report.Components))
	keys := make([]string, 0, len(report.Components))
	for _, key := range report.ComponentOrder {
		if _, exists := report.Components[key]; exists && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	var remaining []string
	for key := range report.Components {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	return append(keys, remaining...)
}

func buildWorkspaceScopes(report introspect.InspectionReport) []workspaceScope {
	keys := orderedScopeCharts(report)
	var packages []string
	known := make(map[string]bool)
	addPackage := func(name string) {
		if name != "" && !known[name] {
			known[name] = true
			packages = append(packages, name)
		}
	}
	if report.Bundle != nil {
		for _, pkg := range report.Bundle.Packages {
			addPackage(pkg.ID)
		}
	}
	for _, pkg := range report.PackageValues {
		addPackage(pkg.PackageName)
	}
	for _, override := range report.UDSOverrideValues {
		addPackage(override.PackageName)
	}
	for _, override := range report.UDSOverrideVariables {
		addPackage(override.PackageName)
	}
	owners := make(map[string]string, len(keys))
	prefixed := false
	for _, key := range keys {
		parts := strings.Split(key, "/")
		if len(parts) == 3 {
			owners[key] = parts[0]
			addPackage(parts[0])
			prefixed = true
		}
	}
	// Direct Zarf reports use component/chart, while bundle reports prefix
	// their package IDs. Never reinterpret a direct component as a package.
	for _, key := range keys {
		if len(strings.Split(key, "/")) != 2 {
			continue
		}
		if report.Bundle == nil && !prefixed && len(report.UDSOverrideValues) == 0 && len(report.UDSOverrideVariables) == 0 && len(packages) == 1 {
			owners[key] = packages[0]
			continue
		}
		owner, ambiguous := "", false
		for _, pkg := range report.PackageValues {
			for _, usage := range pkg.Usages {
				if usage.ChartName == "" || usage.ComponentName+"/"+usage.ChartName != key {
					continue
				}
				if _, exists := report.Components[pkg.PackageName+"/"+key]; exists {
					continue
				}
				if owner != "" && owner != pkg.PackageName {
					ambiguous = true
				}
				owner = pkg.PackageName
			}
		}
		if !ambiguous {
			owners[key] = owner
		}
	}
	scopes := []workspaceScope{{key: "All Charts", label: "All settings", kind: "inspection", charts: len(keys), chartKeys: keys}}
	if report.Bundle != nil {
		scopes = append(scopes, workspaceScope{key: "bundle:inputs", label: "Bundle inputs", kind: "bundle", parentKey: "All Charts", depth: 1})
	}
	appendChildren := func(parent, owner string, depth int, charts []string) {
		var groups []string
		byComponent := make(map[string][]string)
		for _, key := range charts {
			component, _, hasComponent := strings.Cut(key, "/")
			if hasComponent {
				component = key[:strings.LastIndex(key, "/")]
			} else {
				component = ""
			}
			if _, seen := byComponent[component]; !seen {
				groups = append(groups, component)
			}
			byComponent[component] = append(byComponent[component], key)
		}
		for _, component := range groups {
			chartParent, chartDepth := parent, depth
			if component != "" {
				chartParent = "component:" + component
				name := component[strings.LastIndex(component, "/")+1:]
				scopes = append(scopes, workspaceScope{key: chartParent, label: name, kind: "component", parentKey: parent,
					packageName: owner, depth: depth, charts: len(byComponent[component]), chartKeys: byComponent[component]})
				chartDepth++
			}
			for _, key := range byComponent[component] {
				label := key[strings.LastIndex(key, "/")+1:]
				if result := report.Components[key]; result != nil && result.ChartName != "" {
					label = result.ChartName
				}
				if label == "" {
					label = "Standalone chart"
				}
				scopes = append(scopes, workspaceScope{key: key, label: label, kind: "chart", parentKey: chartParent,
					packageName: owner, depth: chartDepth, charts: 1, chartKeys: []string{key}})
			}
		}
	}
	for _, owner := range packages {
		var charts []string
		for _, key := range keys {
			if owners[key] == owner {
				charts = append(charts, key)
			}
		}
		parent := "package:" + owner
		scopes = append(scopes, workspaceScope{key: parent, label: owner, kind: "package", parentKey: "All Charts",
			packageName: owner, depth: 1, charts: len(charts), chartKeys: charts})
		appendChildren(parent, owner, 2, charts)
	}
	var unowned []string
	for _, key := range keys {
		if owners[key] == "" {
			unowned = append(unowned, key)
		}
	}
	appendChildren("All Charts", "", 1, unowned)
	return scopes
}

func (m Model) workspaceScope(key string) (workspaceScope, bool) {
	for _, scope := range m.scopes {
		if scope.key == key {
			return scope, true
		}
	}
	return workspaceScope{}, false
}

func (m Model) chartInScope(key string) bool {
	if m.currentChart == "All Charts" {
		return true
	}
	if scope, found := m.workspaceScope(m.currentChart); found {
		return slices.Contains(scope.chartKeys, key)
	}
	// Older callers can construct a Model without a compiled hierarchy.
	switch {
	case m.currentChart == "bundle:inputs":
		return false
	case strings.HasPrefix(m.currentChart, "package:"):
		owner, _, _ := parseComponentKey(key)
		return owner == strings.TrimPrefix(m.currentChart, "package:")
	case strings.HasPrefix(m.currentChart, "component:"):
		end := strings.LastIndex(key, "/")
		return end >= 0 && key[:end] == strings.TrimPrefix(m.currentChart, "component:")
	default:
		return key == m.currentChart || (m.inputMode == InputModeChartSelect && strings.Contains(strings.ToLower(key), strings.ToLower(m.currentChart)))
	}
}

func (m Model) scopeItemOwner(item DisplayItem) string {
	if item.PackageName != "" {
		return item.PackageName
	}
	if item.IsSummaryItem {
		owner, _, _ := parseComponentKey(item.SummaryComponent)
		return owner
	}
	if scope, found := m.workspaceScope(item.Component); found && scope.kind == "chart" {
		return scope.packageName
	}
	if item.IsPkgVar {
		owner, sole, count, prefixed := "", "", 0, false
		for _, scope := range m.scopes {
			if scope.kind == "package" {
				sole, count = scope.packageName, count+1
				if len(scope.packageName) > len(owner) && strings.HasPrefix(item.Path, scope.packageName+".") {
					owner = scope.packageName
				}
			}
			prefixed = prefixed || scope.kind == "chart" && len(strings.Split(scope.key, "/")) == 3
		}
		if owner == "" && m.bundle == nil && count == 1 && !prefixed && len(m.report.UDSOverrideValues) == 0 && len(m.report.UDSOverrideVariables) == 0 {
			owner = sole
		}
		return owner
	}
	return ""
}

func (m Model) itemInScope(item DisplayItem) bool {
	if m.currentChart == "All Charts" {
		return true
	}
	if m.currentChart == "bundle:inputs" {
		return item.IsBundleVariable || item.IsPackageValue && item.Component == "UDS Next bundle"
	}
	if item.IsBundleVariable {
		return false
	}
	owner := m.scopeItemOwner(item)
	if strings.HasPrefix(m.currentChart, "package:") {
		return owner == strings.TrimPrefix(m.currentChart, "package:") || m.chartInScope(item.Component)
	}
	if item.IsSummaryItem {
		return m.chartInScope(item.SummaryComponent)
	}
	if item.IsPkgVar {
		if scope, found := m.workspaceScope(m.currentChart); found && owner != "" {
			return owner == scope.packageName
		}
		return owner == "" && !m.nextRail
	}
	if item.mappingRef != nil {
		return m.chartInScope(item.mappingRef.chartKey)
	}
	return m.chartInScope(item.Component)
}
func (m Model) scopeLabel() string {
	for _, scope := range m.scopes {
		if scope.key == m.currentChart {
			if scope.kind == "bundle" || scope.kind == "inspection" {
				return scope.label
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
	if m.focusPane != FocusComponents {
		m.scopeReturnFocus = m.focusPane
	}
	m.showOverview = false
	if m.windowWidth >= wideLayoutWidth {
		m.inputMode = InputModeNormal
		m.chartInput.Blur()
		m.focusPane = FocusComponents
		if _, found := m.workspaceScope(m.scopeTreeKey); !found {
			m.syncScopeCursor()
		}
		m.statusMessage = ""
		return
	}
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
		m.focusPane = m.scopeReturnFocus
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
			m.setScopeTreeKey(choices[clamp(m.scopeCursor, 0, len(choices)-1)].key)
			m.inputMode = InputModeNormal
			m.chartInput.Blur()
			m.applyScopeCursor()
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
				owner := m.scopeItemOwner(item)
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
				matched = m.settingLayerMatches(item, value)
			case "status":
				switch value {
				case "changed", "overridden":
					matched = m.valueChanged(item)
				case "runtime", "unresolved":
					matched = m.valueUnresolved(item)
				case "excluded", "unmapped", "mapped", "mixed":
					matched = item.settingChildren == 0 && item.MappingStatus == value
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
	m.showOverview = false
	m.mapping = nil
	m.mappingRowDetail = false
	m.focusPane = FocusValues
	m.updateFilteredValues()
	m.cursor, m.viewportOffset = 0, 0
	m.syncInspectorSelection()
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
