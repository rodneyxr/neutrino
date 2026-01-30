package introspect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

var zarfVarRegex = regexp.MustCompile("###ZARF_VAR_([A-Z0-9_]+)###")

// FindZarfVariables finds all Zarf variables in the content and returns their locations.
func FindZarfVariables(content []byte, fileName string) map[string][]ValueUsageLocation {
	matches := zarfVarRegex.FindAllIndex(content, -1)
	if matches == nil {
		return nil
	}

	results := make(map[string][]ValueUsageLocation)
	contentStr := string(content)

	for _, loc := range matches {
		start := loc[0]
		end := loc[1]
		// Extract the variable name (group 1)
		// loc[0] is start of match, loc[1] is end of match
		// We need to find the submatch... FindAllStringSubmatchIndex would be better but regular FindAllIndex gives us the full match range.
		// Let's re-run match on just the substring? Or use FindAllSubmatchIndex.
		// FindAllSubmatchIndex returns: [start, end, group1Start, group1End...]
		// Let's switch to FindAllSubmatchIndex logic below.
		_ = start
		_ = end
	}

	// Reset and do it properly with SubmatchIndex
	allMatches := zarfVarRegex.FindAllSubmatchIndex(content, -1)
	for _, m := range allMatches {
		// m[0], m[1] is the full match
		// m[2], m[3] is the first group (variable name)
		if len(m) < 4 {
			continue
		}

		fullMatchStart := m[0]
		varName := contentStr[m[2]:m[3]]

		// calculate line and column
		line := strings.Count(contentStr[:fullMatchStart], "\n") + 1

		lastNewLine := strings.LastIndex(contentStr[:fullMatchStart], "\n")
		col := fullMatchStart + 1 // 1-based, assuming start of file
		if lastNewLine != -1 {
			col = fullMatchStart - lastNewLine
		}

		location := ValueUsageLocation{
			File:   fileName,
			Line:   line,
			Column: col,
			Kind:   "zarf_var",
		}

		results[varName] = append(results[varName], location)
	}

	return results
}

// ParseValuesLocations parses values.yaml content and returns a map of paths to their locations.
func ParseValuesLocations(content []byte) (map[string]ValueUsageLocation, error) {
	file, err := parser.ParseBytes(content, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse values.yaml: %w", err)
	}

	locations := make(map[string]ValueUsageLocation)

	var walk func(node ast.Node, prefix string)
	walk = func(node ast.Node, prefix string) {
		switch n := node.(type) {
		case *ast.MappingNode:
			for _, value := range n.Values {
				key := value.Key.String()
				currentPath := key
				if prefix != "" {
					currentPath = prefix + "." + key
				}

				// Store location of the key (or the value? User wants to jump to definition)
				// Usually jumping to the key definition is good.
				line := value.Key.GetToken().Position.Line
				col := value.Key.GetToken().Position.Column

				locations[currentPath] = ValueUsageLocation{
					File:   "values.yaml",
					Line:   line,
					Column: col,
					Kind:   "value",
				}

				walk(value.Value, currentPath)
			}
		case *ast.MappingValueNode:
			// Already handled in MappingNode
		case *ast.DocumentNode:
			walk(n.Body, prefix)
		}
	}

	for _, doc := range file.Docs {
		walk(doc.Body, "")
	}

	return locations, nil
}

// getCacheDir returns the directory where the artifact should be cached.
// It uses a hash of the URL and version to create a unique path under the category.
func getCacheDir(category string, url string, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home dir: %w", err)
	}

	cacheBase := filepath.Join(home, ".cache", "neutrino", category)

	// Create a unique hash for the chart source
	hasher := sha256.New()
	hasher.Write([]byte(url))
	hasher.Write([]byte(version))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return filepath.Join(cacheBase, hash), nil
}

// GetValue looks up a value in a map using dot notation.
// It supports fallback keys (e.g. _internal_defaults_do_not_set) if the direct path is not found.
func GetValue(values map[string]interface{}, path string) interface{} {
	// 1. Try direct path
	if val := getValueStrict(values, path); val != nil {
		return val
	}

	// 2. Try fallbacks
	// These are common wrapper keys used in some Zarf/UDS packages where the values.yaml
	// namespaces the values (e.g. for subcharts or internal defaults) but the templates
	// might access them differently or expects them to be merged.
	fallbacks := []string{"_internal_defaults_do_not_set"}

	for _, prefix := range fallbacks {
		// Check if the prefix key exists in values
		if _, ok := values[prefix]; ok {
			// Try looking up path under prefix
			if val := getValueStrict(values, prefix+"."+path); val != nil {
				return val
			}
		}
	}

	return nil
}

func getValueStrict(values map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := values

	for i, part := range parts {
		if i == len(parts)-1 {
			return current[part]
		}

		val, ok := current[part]
		if !ok {
			return nil
		}

		if nextMap, ok := val.(map[string]interface{}); ok {
			current = nextMap
		} else {
			return nil
		}
	}

	return nil
}

// FlattenToPaths recursively finds all dot-notation paths in a map.
func FlattenToPaths(prefix string, value interface{}) []string {
	var paths []string

	if m, ok := value.(map[string]interface{}); ok {
		for k, v := range m {
			newPath := k
			if prefix != "" {
				newPath = prefix + "." + k
			}

			// Add the current path itself (it might be a leaf or a branch)
			paths = append(paths, newPath)

			// Recurse
			paths = append(paths, FlattenToPaths(newPath, v)...)
		}
	}
	// Note: We deliberately stop at arrays and primitives, consistent with the goal
	// of finding addressable keys in values.yaml.

	return paths
}

// FindChartRoot walks the directory to find the directory containing Chart.yaml.
// Prioritizes the root, then immediate subdirectories.
func FindChartRoot(dir string) (string, error) {
	// 1. Check root
	if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err == nil {
		return dir, nil
	}

	// 2. Check immediate subdirectories
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			candidate := filepath.Join(dir, entry.Name())
			if _, err := os.Stat(filepath.Join(candidate, "Chart.yaml")); err == nil {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("no Chart.yaml found in %s or immediate subdirectories", dir)
}
