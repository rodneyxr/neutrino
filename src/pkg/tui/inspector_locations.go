package tui

import (
	"fmt"
	"path"
	"sort"
	"strconv"
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
	if source.sourceRole != "" {
		coordinate := terminalText(source.sourceDisplayPath)
		if coordinate == "" {
			coordinate = "File unavailable"
			if line > 0 {
				coordinate += fmt.Sprintf(" · line %d", line)
			}
		} else if line > 0 {
			coordinate += fmt.Sprintf(":%d", line)
		}
		return coordinate + " · " + terminalText(source.sourceRole)
	}
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

// labelInspectorSources shortens the complete visible source set before any
// preview limit is applied. Only labels change: source paths and coordinates
// remain the recorded editor targets, including when two cache views contain
// the same logical filename.
func (m Model) labelInspectorSources(sources []DetailItem) []DetailItem {
	if sources == nil {
		return nil
	}
	result := append([]DetailItem(nil), sources...)
	type sourcePath struct {
		parts []string
		depth int
		label string
	}
	var paths []sourcePath
	byIdentity := map[string]int{}
	indices := make([]int, len(sources))
	for i, source := range sources {
		file, _ := inspectorSourceCoordinates(source)
		identity := file
		// Relative filenames are local to their recorded chart owner.
		if !inspectorAbsolutePath(file) {
			identity += "\x00" + source.sourceComponent
		}
		index, found := byIdentity[identity]
		if !found {
			index = len(paths)
			byIdentity[identity] = index
			parts := m.inspectorSourcePathParts(source, file)
			paths = append(paths, sourcePath{parts: parts, depth: min(1, len(parts))})
		}
		indices[i] = index
	}
	for {
		groups := map[string][]int{}
		for i := range paths {
			p := &paths[i]
			p.label = strings.Join(p.parts[len(p.parts)-p.depth:], "/")
			groups[p.label] = append(groups[p.label], i)
		}
		expanded := false
		for _, group := range groups {
			if len(group) < 2 {
				continue
			}
			for _, i := range group {
				if paths[i].depth < len(paths[i].parts) {
					paths[i].depth++
					expanded = true
				}
			}
		}
		if expanded {
			continue
		}
		// Distinct files can have identical logical paths after cache roots are
		// removed. An ordinal disambiguates without inventing package ownership.
		for _, group := range groups {
			if len(group) > 1 {
				for ordinal, i := range group {
					if paths[i].label != "" {
						paths[i].label += fmt.Sprintf(" [source %d]", ordinal+1)
					}
				}
			}
		}
		break
	}
	for i, source := range sources {
		file, line := inspectorSourceCoordinates(source)
		role := source.sourceRole
		if role == "" {
			role = strings.TrimPrefix(inspectorSourceLabel(source), sourceCoordinateLabel(file, line)+" · ")
		}
		result[i].sourceRole = terminalText(role)
		result[i].sourceDisplayPath = paths[indices[i]].label
		result[i].Label = inspectorSourceLabel(result[i])
	}
	return result
}

func (m Model) inspectorSourcePathParts(source DetailItem, file string) []string {
	for _, pkg := range m.report.PackageValues {
		for _, input := range pkg.Sources {
			if file != "" && traceSourceFile(input) == file && input.Name != "" {
				return append(inspectorDisplayPathParts(pkg.PackageName), inspectorDisplayPathParts(input.Name)...)
			}
		}
	}
	if m.bundle != nil {
		for _, input := range m.bundle.VariableSources {
			if file != "" && traceSourceFile(input) == file && input.Name != "" {
				return inspectorDisplayPathParts(input.Name)
			}
		}
	}
	// Prefer the longest verified chart root. Sorting makes equal-root aliases
	// deterministic; an explicitly recorded owner takes precedence among them.
	keys := make([]string, 0, len(m.components))
	for key := range m.components {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cleanFile := inspectorCleanPath(file)
	owner, relative, rootLength := "", "", 0
	for _, key := range keys {
		chart := m.components[key]
		if chart == nil || chart.Source == "" || !inspectorAbsolutePath(file) {
			continue
		}
		root := strings.TrimSuffix(inspectorCleanPath(chart.Source), "/")
		if strings.HasPrefix(cleanFile, root+"/") &&
			(len(root) > rootLength || len(root) == rootLength && key == source.sourceComponent) {
			owner, relative, rootLength = key, strings.TrimPrefix(cleanFile, root+"/"), len(root)
		}
	}
	if owner != "" {
		return append(inspectorDisplayPathParts(owner), inspectorDisplayPathParts(relative)...)
	}
	parts := inspectorDisplayPathParts(file)
	if source.sourceComponent != "" && len(parts) > 0 {
		parts = append(inspectorDisplayPathParts(source.sourceComponent), parts...)
	}
	return parts
}

func inspectorCleanPath(file string) string {
	if strings.TrimSpace(file) == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(terminalText(file), "\\", "/"))
}

func inspectorAbsolutePath(file string) bool {
	file = inspectorCleanPath(file)
	return strings.HasPrefix(file, "/") || len(file) > 2 && file[1] == ':' && file[2] == '/'
}

// inspectorDisplayPathParts returns suffix candidates, not a guessed project
// root. Cache transport directories and opaque digests never become labels.
func inspectorDisplayPathParts(file string) []string {
	clean := inspectorCleanPath(file)
	if clean == "" || clean == "." {
		return nil
	}
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if inspectorAbsolutePath(file) && len(parts) > 1 {
		if strings.HasSuffix(parts[0], ":") {
			parts = parts[1:]
		}
		switch strings.ToLower(parts[0]) {
		case "home", "users":
			parts = parts[min(2, len(parts)-1):]
		case "tmp", "var", "private", "root", "mnt", "opt":
			parts = parts[1:]
		}
	}
	start := 0
	cache := false
	if inspectorAbsolutePath(file) {
		for i, part := range parts[:len(parts)-1] {
			switch part {
			case ".cache", "cache", "neutrino-cache", "package-views", "package-blobs", "next-packages", "next-controls", "extracted-components":
				start, cache = i+1, true
			case "neutrino":
				if cache {
					start = i + 1
				}
			}
		}
	}
	var display []string
	for i, part := range parts[start:] {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if i < len(parts[start:])-1 {
			_, numberErr := strconv.Atoi(part)
			if cache && (inspectorOpaquePathPart(part) || numberErr == nil) {
				continue
			}
		} else if cache && inspectorOpaquePathPart(part) {
			part = "source file"
		}
		display = append(display, part)
	}
	return display
}

func inspectorOpaquePathPart(part string) bool {
	if strings.HasPrefix(strings.ToLower(part), "sha256") {
		return true
	}
	if len(part) < 8 {
		return false
	}
	for _, r := range part {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
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
