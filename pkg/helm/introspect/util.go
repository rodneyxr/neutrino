package introspect

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
