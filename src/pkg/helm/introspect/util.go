package introspect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/defenseunicorns/pkg/oci"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/zarf-dev/zarf/src/config"
	"github.com/zarf-dev/zarf/src/pkg/logger"
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
				// Skip keys containing dots: they can't be expressed as valid Helm .Values
				// dot-notation path segments (e.g. Kubernetes label/annotation keys like
				// "istio.io/dataplane-mode" or "inject.istio.io/templates").
				if strings.Contains(key, ".") {
					continue
				}
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
			// Skip keys containing dots: they can't be expressed as valid Helm .Values
			// dot-notation path segments (e.g. Kubernetes label/annotation keys like
			// "istio.io/dataplane-mode" or "zarf.dev/agent").
			if strings.Contains(k, ".") {
				continue
			}

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

// IsZarfPackage checks if the source is a Zarf package.
func IsZarfPackage(ctx context.Context, source string, arch string) (bool, error) {
	if strings.HasSuffix(source, ".tar.zst") {
		return true, nil
	}
	if strings.HasPrefix(source, "oci://") {
		l := logger.From(ctx)
		targetArch := config.GetArch(arch)
		platform := oci.PlatformForArch(targetArch)

		isZarf, err := checkZarfRef(ctx, source, platform)
		if err == nil {
			return isZarf, nil
		}
		l.Debug(fmt.Sprintf("IsZarfPackage: failed with target arch %s: %s", targetArch, err))
		var mismatch *platformMismatchError
		if errors.As(err, &mismatch) {
			return false, err
		}
		return false, nil
	}
	return false, nil
}

// IsUDSBundle checks if the source is a UDS bundle.
func IsUDSBundle(ctx context.Context, source string, arch string) (bool, error) {
	if strings.HasSuffix(source, ".tar.zst") {
		return isUDSBundleLocal(ctx, source)
	}
	if strings.HasPrefix(source, "oci://") {
		l := logger.From(ctx)
		targetArch := config.GetArch(arch)
		platform := oci.PlatformForArch(targetArch)

		isUDS, err := checkUDSRef(ctx, source, platform)
		if err == nil {
			return isUDS, nil
		}
		l.Debug(fmt.Sprintf("IsUDSBundle: failed with target arch %s: %s", targetArch, err))

		if platform.Architecture != "amd64" {
			amd64Platform := oci.PlatformForArch("amd64")
			isUDS, err := checkUDSRef(ctx, source, amd64Platform)
			if err == nil {
				if isUDS {
					return true, nil
				}
				return false, nil
			}
			l.Debug(fmt.Sprintf("IsUDSBundle: fallback arch (amd64) check failed: %s", err))
		}
		return false, nil
	}
	return false, nil
}

func isUDSBundleLocal(ctx context.Context, source string) (bool, error) {
	name := filepath.Base(source)
	if strings.Contains(name, "uds-bundle") {
		return true, nil
	}
	return false, nil
}

func checkUDSRef(ctx context.Context, source string, platform ocispec.Platform) (bool, error) {
	remote, err := oci.NewOrasRemote(source, platform)
	if err != nil {
		return false, err
	}
	root, err := remote.FetchRoot(ctx)
	if err != nil {
		return false, err
	}
	for _, layer := range root.Layers {
		if layer.Annotations["org.opencontainers.image.title"] == UDSBundleFile {
			return true, nil
		}
	}
	return false, nil
}

func checkZarfRef(ctx context.Context, source string, platform ocispec.Platform) (bool, error) {
	root, err := newRegistryPackageRemote(source).Resolve(ctx, platform)
	if err != nil {
		return false, err
	}
	return root.Manifest.Config.MediaType == zarfConfigMediaType, nil
}

// IsRemote determines if a source string is a remote URL.
func IsRemote(source string) bool {
	return len(source) > 6 && (source[:6] == "oci://" || source[:7] == "http://" || source[:8] == "https://")
}

// FormatDefaultValue formats a value consistently for display (YAML style, truncated).
func FormatDefaultValue(val interface{}) string {
	if val == nil {
		return "-"
	}
	yamlBytes, err := yaml.MarshalWithOptions(val, yaml.Flow(true))
	if err != nil {
		return fmt.Sprintf("%v", val)
	}
	result := strings.TrimSpace(string(yamlBytes))
	result = strings.ReplaceAll(result, ".0,", ",")
	result = strings.ReplaceAll(result, ".0}", "}")
	result = strings.ReplaceAll(result, ".0]", "]")
	result = strings.TrimSuffix(result, ".0")
	if len(result) > 32 {
		return result[:29] + "..."
	}
	return result
}

// mergeMaps recursively merges two maps. Dest prevails over src for matching keys if both are primitives.
// If both are maps, they are merged.
func mergeMaps(dest, src map[string]interface{}) map[string]interface{} {
	if dest == nil {
		dest = make(map[string]interface{})
	}
	for k, v := range src {
		if destVal, ok := dest[k]; ok {
			if destMap, okDest := destVal.(map[string]interface{}); okDest {
				if srcMap, okSrc := v.(map[string]interface{}); okSrc {
					dest[k] = mergeMaps(destMap, srcMap)
					continue
				}
			}
		}
		// If both aren't maps or dest doesn't have it, overwrite dest with src
		dest[k] = v
	}
	return dest
}
