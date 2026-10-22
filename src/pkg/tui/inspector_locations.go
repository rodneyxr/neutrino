package tui

import (
	"fmt"
	"strings"
)

// sourceCoordinateLabel presents recorded coordinates without guessing a file
// or replacing an unknown line with line 1. Retaining the complete path keeps
// sources with the same basename distinguishable; callers may wrap this label.
func sourceCoordinateLabel(file string, line int) string {
	file = terminalText(file)
	if strings.TrimSpace(file) == "" {
		if line > 0 {
			return fmt.Sprintf("File unavailable · line %d", line)
		}
		return "File unavailable · line unavailable"
	}
	if line > 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return file + " · line unavailable"
}

// inspectorSourceCoordinates uses the action's recorded file target, with the
// original location as a fallback. A line attached to a different file cannot
// be attributed to the target. This helper performs no filesystem lookup.
func inspectorSourceCoordinates(source DetailItem) (string, int) {
	file := source.File
	if file == "" {
		file = source.Location.File
	}
	line := source.Location.Line
	if source.Location.File != "" && source.Location.File != file {
		line = 0
	}
	return file, line
}

// inspectorSourceLabel puts the actionable filename and line before the role.
// Label may carry the package/input role supplied by friendlySourceLabel; when
// absent, the recorded location kind supplies the role. The helper is
// idempotent so a summary preview can reuse already-labeled inspectorSources.
func inspectorSourceLabel(source DetailItem) string {
	file, line := inspectorSourceCoordinates(source)
	coordinate := sourceCoordinateLabel(file, line)
	role := strings.TrimSpace(terminalText(source.Label))
	if role == coordinate || strings.HasPrefix(role, coordinate+" · ") {
		return role
	}
	if role == "" {
		role = "Values source"
		if source.Kind == DetailItemLocation || source.Location.Kind != "" {
			role = sourceRole(source.Location)
		}
		if source.sourceComponent != "" {
			_, component, chart := parseComponentKey(source.sourceComponent)
			owner := strings.Trim(component+"/"+chart, "/")
			if owner != "" {
				role = owner + " · " + role
			}
		}
	}
	return coordinate + " · " + terminalText(role)
}

// inspectorSourcePreviews returns at most limit source labels, preserving the
// inspectorSources order. Each preview corresponds to the same source index;
// the caller retains the total count and the actions for further navigation.
func inspectorSourcePreviews(sources []DetailItem, limit int) []string {
	if limit <= 0 || len(sources) == 0 {
		return nil
	}
	previews := make([]string, 0, min(limit, len(sources)))
	for _, source := range sources[:min(limit, len(sources))] {
		previews = append(previews, inspectorSourceLabel(source))
	}
	return previews
}
