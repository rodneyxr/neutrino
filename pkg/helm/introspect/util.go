package introspect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
func GetValue(values map[string]interface{}, path string) interface{} {
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
