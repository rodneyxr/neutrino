package introspect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// getCacheDir returns the directory where the chart should be cached.
// It uses a hash of the URL and version to create a unique path.
func getCacheDir(url string, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home dir: %w", err)
	}

	cacheBase := filepath.Join(home, ".cache", "neutrino", "charts")

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
