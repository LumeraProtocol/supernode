package version

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/utils"
)

// Manager handles version storage and symlink management
type Manager struct {
	homeDir string
}

// NewManager creates a new version manager
func NewManager(homeDir string) *Manager {
	return &Manager{
		homeDir: homeDir,
	}
}

// GetBinariesDir returns the binaries directory path
func (m *Manager) GetBinariesDir() string {
	return filepath.Join(m.homeDir, "binaries")
}

// GetVersionDir returns the directory path for a specific version
func (m *Manager) GetVersionDir(version string) string {
	return filepath.Join(m.GetBinariesDir(), version)
}

// GetVersionBinary returns the binary path for a specific version
func (m *Manager) GetVersionBinary(version string) string {
	return filepath.Join(m.GetVersionDir(version), "supernode")
}

// GetCurrentLink returns the path to the current symlink
func (m *Manager) GetCurrentLink() string {
	return filepath.Join(m.homeDir, "current")
}

// IsVersionInstalled checks if a version is already installed
func (m *Manager) IsVersionInstalled(version string) bool {
	binary := m.GetVersionBinary(version)
	_, err := os.Stat(binary)
	return err == nil
}

// InstallVersion installs a binary to the version directory
func (m *Manager) InstallVersion(version string, binaryPath string) error {
	// Create version directory
	versionDir := m.GetVersionDir(version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}

	// Destination binary path
	destBinary := m.GetVersionBinary(version)

	// Copy binary to version directory
	input, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to read binary: %w", err)
	}

	if err := os.WriteFile(destBinary, input, 0755); err != nil {
		return fmt.Errorf("failed to write binary: %w", err)
	}

	return nil
}

// SetCurrentVersion updates the current symlink to point to a version
func (m *Manager) SetCurrentVersion(version string) error {
	// Verify version exists
	if !m.IsVersionInstalled(version) {
		return fmt.Errorf("version %s is not installed", version)
	}

	currentLink := m.GetCurrentLink()
	targetDir := m.GetVersionDir(version)

	// Remove existing symlink if it exists
	if err := os.RemoveAll(currentLink); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing symlink: %w", err)
	}

	// Create new symlink
	if err := os.Symlink(targetDir, currentLink); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}

// GetCurrentVersion returns the currently active version
func (m *Manager) GetCurrentVersion() (string, error) {
	currentLink := m.GetCurrentLink()

	// Read the symlink
	target, err := os.Readlink(currentLink)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no version currently set")
		}
		return "", fmt.Errorf("failed to read current version: %w", err)
	}

	// Extract version from path
	version := filepath.Base(target)
	return version, nil
}

// ListVersions returns all installed versions
func (m *Manager) ListVersions() ([]string, error) {
	binariesDir := m.GetBinariesDir()

	entries, err := os.ReadDir(binariesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read binaries directory: %w", err)
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it contains a supernode binary
			binaryPath := filepath.Join(binariesDir, entry.Name(), "supernode")
			if _, err := os.Stat(binaryPath); err == nil {
				versions = append(versions, entry.Name())
			}
		}
	}

	// Sort versions (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return utils.CompareVersions(versions[i], versions[j]) > 0
	})

	return versions, nil
}

// CleanupOldVersions removes old versions, keeping the specified number
func (m *Manager) CleanupOldVersions(keepCount int) error {
	if keepCount < 1 {
		keepCount = 1
	}

	versions, err := m.ListVersions()
	if err != nil {
		return fmt.Errorf("failed to list versions: %w", err)
	}

	if len(versions) <= keepCount {
		return nil // Nothing to clean up
	}

	// Get current version to avoid deleting it
	current, _ := m.GetCurrentVersion()

	// Versions are already sorted (newest first)
	kept := 0
	for _, version := range versions {
		if version == current || kept < keepCount {
			kept++
			continue
		}

		// Remove this version
		versionDir := m.GetVersionDir(version)
		if err := os.RemoveAll(versionDir); err != nil {
			return fmt.Errorf("failed to remove version %s: %w", version, err)
		}
		fmt.Printf("Removed old version: %s\n", version)
	}

	return nil
}
