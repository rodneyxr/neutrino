package tui

import (
	"sort"
	"strings"

	"github.com/defenseunicorns-dashdays/neutrino/src/pkg/helm/introspect"
)

func configurationStatusLabel(status introspect.ValueStatus) string {
	if status == introspect.ValueDynamic {
		return "Needs evaluation"
	}
	return traceStatus(status)
}

// Errors need operator action; a dynamic input needs evaluation outside the
// static inspector. Keep those causes distinct even when both affect a path.
func configurationStatusPriority(status introspect.ValueStatus) int {
	switch status {
	case introspect.ValueParseError:
		return 4
	case introspect.ValueUnavailable:
		return 3
	case introspect.ValueMissing:
		return 2
	case introspect.ValueDynamic:
		return 1
	}
	if unresolvedStatus(status) {
		return 1
	}
	return 0
}

func configurationMappingStatus(mapping *valueMapping) introspect.ValueStatus {
	if mapping == nil {
		return ""
	}
	status := configurationUsageStatus(mapping.usage)
	include := func(candidate introspect.ValueStatus) {
		if configurationStatusPriority(candidate) > configurationStatusPriority(status) {
			status = candidate
		}
	}
	if mapping.usage.Runtime {
		include(introspect.ValueDynamic)
	}
	if mapping.pkg != nil {
		for _, source := range mapping.pkg.Sources {
			include(source.Status)
			for _, diagnostic := range source.Diagnostics {
				include(diagnostic.Status)
			}
		}
	}
	return status
}

func configurationUsageStatus(usage introspect.PackageValueUsage) introspect.ValueStatus {
	status := usage.Status
	include := func(candidate introspect.ValueStatus) {
		if configurationStatusPriority(candidate) > configurationStatusPriority(status) {
			status = candidate
		}
	}
	if usage.Runtime {
		include(introspect.ValueDynamic)
	}
	for _, location := range usage.Locations {
		switch location.Kind {
		case "unavailableTemplate":
			include(introspect.ValueUnavailable)
		case "unparsedTemplate":
			include(introspect.ValueParseError)
		}
	}
	return status
}

func configurationDiagnostics(usage *introspect.PackageValueUsage, sources []introspect.ValueSource) []string {
	type issue struct {
		introspect.ValueDiagnostic
		file string
	}
	var sourceLabels []DetailItem
	for _, source := range sources {
		sourceLabels = append(sourceLabels, DetailItem{File: source.Name})
	}
	sourceLabels = (Model{}).labelInspectorSources(sourceLabels)
	var diagnostics []issue
	for i, source := range sources {
		file := ""
		if source.Name != "" {
			file = sourceLabels[i].sourceDisplayPath
		}
		hasReason := false
		for _, diagnostic := range source.Diagnostics {
			if unresolvedStatus(diagnostic.Status) {
				diagnostics = append(diagnostics, issue{ValueDiagnostic: diagnostic, file: file})
				hasReason = true
			}
		}
		if !hasReason && unresolvedStatus(source.Status) {
			diagnostics = append(diagnostics, issue{ValueDiagnostic: introspect.ValueDiagnostic{Status: source.Status}, file: file})
		}
	}
	if usage != nil && unresolvedStatus(configurationUsageStatus(*usage)) {
		status := configurationUsageStatus(*usage)
		message := usage.Diagnostic
		if message == "" {
			for _, location := range usage.Locations {
				switch location.Kind {
				case "unavailableTemplate":
					message = "Template content was not available for inspection."
				case "unparsedTemplate":
					message = "Template could not be parsed."
				}
			}
		}
		diagnostics = append(diagnostics, issue{ValueDiagnostic: introspect.ValueDiagnostic{Status: status, Path: usage.Path, Message: message}})
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		return configurationStatusPriority(diagnostics[i].Status) > configurationStatusPriority(diagnostics[j].Status)
	})
	var reasons []string
	seen := make(map[string]bool)
	for _, diagnostic := range diagnostics {
		message := strings.TrimSpace(diagnostic.Message)
		if message == "" {
			switch diagnostic.Status {
			case introspect.ValueMissing:
				message = "Configuration input was not supplied."
			case introspect.ValueParseError:
				message = "Configuration input could not be parsed."
			case introspect.ValueUnavailable:
				message = "Configuration input could not be inspected."
			case introspect.ValueDynamic:
				message = "Requires evaluation beyond static inspection."
			default:
				message = "Inspection did not resolve this input."
			}
		}
		var context []string
		if diagnostic.file != "" {
			context = append(context, diagnostic.file)
		}
		if diagnostic.Path != "" {
			label := diagnostic.Path
			if inspectorAbsolutePath(label) {
				short := (Model{}).labelInspectorSources([]DetailItem{{File: label}})
				label = short[0].sourceDisplayPath
			}
			context = append(context, label)
		}
		if len(context) > 0 {
			message = strings.Join(context, " · ") + ": " + message
		}
		reason := configurationStatusLabel(diagnostic.Status) + ": " + message
		if !seen[reason] {
			seen[reason] = true
			reasons = append(reasons, reason)
		}
	}
	return reasons
}

func configurationMappingDiagnostics(mapping *valueMapping) []string {
	if mapping == nil {
		return nil
	}
	var sources []introspect.ValueSource
	if mapping.pkg != nil {
		sources = mapping.pkg.Sources
	}
	return configurationDiagnostics(&mapping.usage, sources)
}

// configurationProblems also covers runtime-only observations and packages
// without static mappings, so the compact inspector never hides their causes.
func (m Model) configurationProblems(value DisplayItem) []string {
	var sources []introspect.ValueSource
	if value.IsBundleVariable && m.bundle != nil {
		sources = m.bundle.VariableSources
	} else if pkg := m.tracePackage(value); pkg != nil {
		sources = pkg.Sources
	}
	reasons := configurationDiagnostics(nil, sources)
	seen := make(map[string]bool)
	for _, reason := range reasons {
		seen[reason] = true
	}
	seenRuntimeCauses := make(map[string]bool)
	add := func(mapping *valueMapping) {
		if mapping == nil {
			return
		}
		var diagnostics []string
		if mapping.pkg != nil {
			diagnostics = configurationDiagnostics(nil, mapping.pkg.Sources)
		}
		usage := mapping.usage
		status := configurationUsageStatus(usage)
		// The source-specific errors above already explain this generic
		// downstream consequence. Keep concrete usage failures separately.
		genericSourceFailure := usage.Diagnostic == "An unresolved values input may affect this mapping source." && len(diagnostics) > 0
		repeatedRuntimeCause := status == introspect.ValueDynamic && usage.Diagnostic != "" && seenRuntimeCauses[usage.Diagnostic]
		genericSelectedRuntime := runtimeReference(value) && status == introspect.ValueDynamic && usage.Diagnostic == ""
		if !genericSourceFailure && !repeatedRuntimeCause && !genericSelectedRuntime {
			diagnostics = append(diagnostics, configurationDiagnostics(&usage, nil)...)
		}
		if status == introspect.ValueDynamic && usage.Diagnostic != "" {
			seenRuntimeCauses[usage.Diagnostic] = true
		}
		for _, reason := range diagnostics {
			if !seen[reason] {
				seen[reason] = true
				reasons = append(reasons, reason)
			}
		}
	}
	add(value.mappingRef)
	for _, route := range value.Routes {
		add(route.mapping)
	}
	for _, mapping := range m.configurationMappings(value) {
		add(mapping)
	}
	priority := func(reason string) int {
		for _, status := range []introspect.ValueStatus{introspect.ValueParseError, introspect.ValueUnavailable, introspect.ValueMissing, introspect.ValueDynamic} {
			if strings.HasPrefix(reason, configurationStatusLabel(status)+":") {
				return configurationStatusPriority(status)
			}
		}
		return 0
	}
	sort.SliceStable(reasons, func(i, j int) bool { return priority(reasons[i]) > priority(reasons[j]) })
	return reasons
}
