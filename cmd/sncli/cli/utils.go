package cli

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

func NormalizePath(path string) string {
	// expand environment variables if any
	path = os.ExpandEnv(path)
	// replaces ~ with the user's home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("unable to resolve home directory: %v", err)
		}
		path = filepath.Join(home, path[1:])
	}
	path = filepath.Clean(path)
	return path
}

func processConfigPath(path string) string {
	path = NormalizePath(path)
	// check if path defines directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, defaultConfigFileName)
	}
	path = filepath.Clean(path)
	return path
}
