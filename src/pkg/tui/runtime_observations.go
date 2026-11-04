package tui

import (
	"fmt"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

// Observations name real template consumers, including those whose generated
// output paths are unknown. They never become inferred Helm destinations.
func (m Model) observedValueUsages(value DisplayItem) []introspect.PackageValueUsage {
	if value.IsPackageMapping && value.mappingRef != nil {
		usage := value.mappingRef.usage
		if usage.Runtime || usage.TargetPath == "" {
			return []introspect.PackageValueUsage{usage}
		}
		return nil
	}
	if !value.IsPackageValue || value.Component == "UDS Next bundle" {
		return nil
	}
	pkg := m.tracePackage(value)
	if pkg == nil {
		return nil
	}
	var uses []introspect.PackageValueUsage
	for _, usage := range pkg.Usages {
		if !usage.Runtime && usage.TargetPath != "" {
			continue
		}
		if referencesPackageInput(usage) && tracePathsOverlap(value.Path, usage.Path) {
			uses = append(uses, usage)
		}
	}
	return uses
}

func observedUsageOwner(value DisplayItem, usage introspect.PackageValueUsage) string {
	return strings.Trim(strings.Join([]string{value.PackageName, usage.ComponentName, usage.ChartName}, "/"), "/")
}

// Unparsed/unavailable templates use their filename as the reference identity.
// Shorten that display without altering the path searched, copied or opened.
func (m Model) referenceDisplayPath(value DisplayItem) string {
	if !runtimeReference(value) || !inspectorAbsolutePath(value.Path) {
		return value.Path
	}
	var sources []DetailItem
	selected := -1
	for _, candidate := range m.allValues {
		if !runtimeReference(candidate) || !inspectorAbsolutePath(candidate.Path) {
			continue
		}
		if candidate.Path == value.Path && candidate.PackageName == value.PackageName {
			selected = len(sources)
		}
		sources = append(sources, DetailItem{File: candidate.Path, sourceComponent: candidate.PackageName})
	}
	if selected < 0 {
		selected = len(sources)
		sources = append(sources, DetailItem{File: value.Path, sourceComponent: value.PackageName})
	}
	return m.labelInspectorSources(sources)[selected].sourceDisplayPath
}

func (m Model) observedUsesDocument(value DisplayItem, doc inspectorDocument) inspectorDocument {
	doc.kind = "Template uses"
	doc.lines = append(doc.lines, inspectorLine{text: "Recorded references; runtime output paths are not evaluated.", prose: true})
	seenReasons := map[string]bool{}
	// Group observations by consumer and de-duplicate coordinates. A parent
	// object read can overlap a leaf read at the same template location.
	var uses []introspect.PackageValueUsage
	groups := make(map[string]int)
	seenCoordinates := make(map[string]bool)
	for _, usage := range m.observedValueUsages(value) {
		owner := usage.ComponentName + "/" + usage.ChartName
		groupKey := owner + "\x00" + usage.TargetPath + "\x00" + usage.Diagnostic + "\x00" + string(configurationUsageStatus(usage))
		index, found := groups[groupKey]
		if !found {
			index = len(uses)
			groups[groupKey] = index
			copy := usage
			copy.Locations = nil
			uses = append(uses, copy)
		}
		for _, location := range usage.Locations {
			// The template-use page is a file/line index. Distinct column
			// observations remain in Sources and the immutable report.
			key := fmt.Sprintf("%s:%s:%d", groupKey, location.File, location.Line)
			if !seenCoordinates[key] {
				seenCoordinates[key] = true
				uses[index].Locations = append(uses[index].Locations, location)
			}
		}
	}
	var sources []DetailItem
	for _, usage := range uses {
		for _, location := range usage.Locations {
			sources = append(sources, DetailItem{Kind: DetailItemSource, File: location.File,
				Location: location, Label: "Template use", sourceComponent: observedUsageOwner(value, usage)})
		}
	}
	sources = m.labelInspectorSources(sources)
	sourceIndex := 0
	for _, usage := range uses {
		owner := strings.Trim(usage.ComponentName+"/"+usage.ChartName, "/")
		if usage.ComponentName == usage.ChartName {
			owner = usage.ComponentName
		}
		if owner != "" {
			doc.lines = append(doc.lines, inspectorLine{text: owner, section: true})
		}
		if usage.Diagnostic != "" && !seenReasons[usage.Diagnostic] {
			doc.lines = append(doc.lines, inspectorLine{text: usage.Diagnostic, prose: true})
			seenReasons[usage.Diagnostic] = true
		}
		if usage.TargetPath != "" {
			doc.lines = append(doc.lines, inspectorLine{label: "Declared target", text: usage.TargetPath})
		}
		if len(usage.Locations) == 0 {
			doc.lines = append(doc.lines, inspectorLine{text: "Source location unavailable"})
			continue
		}
		for range usage.Locations {
			detail := sources[sourceIndex]
			sourceIndex++
			doc.lines = append(doc.lines, inspectorLine{text: detail.Label, action: &detail})
		}
	}
	return doc
}
