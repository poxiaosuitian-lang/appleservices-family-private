package cachepath

import (
	"errors"
	"os"
	"path/filepath"
)

// Root allows portable installations to keep SAP downloads off the system disk.
func Root() (string, error) {
	if root := os.Getenv("APPLESERVICES_CACHE_DIR"); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("APPLESERVICES_CACHE_DIR must be absolute")
		}
		return filepath.Clean(root), nil
	}
	return os.UserCacheDir()
}
