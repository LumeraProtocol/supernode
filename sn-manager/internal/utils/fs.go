package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

// IsDirWritable returns true if the directory is writable by the current process.
// It attempts to create and remove a temporary file inside the directory.
func IsDirWritable(dir string) (bool, error) {
	if dir == "" {
		return false, fmt.Errorf("empty directory path")
	}
	// Ensure directory exists
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		if err != nil {
			return false, err
		}
		return false, fmt.Errorf("not a directory: %s", dir)
	}
	// Try to create a temp file
	tmpPath := filepath.Join(dir, ".wtest.tmp")
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return false, nil
	}
	_ = f.Close()
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		// Consider directory writable even if cleanup failed, but report error
		return true, err
	}
	return true, nil
}
