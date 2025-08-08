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

// InstallVersion installs a binary to the version directory atomically
func (m *Manager) InstallVersion(version string, binaryPath string) error {
	// Create version directory
	versionDir := m.GetVersionDir(version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}

	// Destination binary path
	destBinary := m.GetVersionBinary(version)
	tempBinary := destBinary + ".tmp"

	// Copy binary to temp location first
	input, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to read binary: %w", err)
	}

	if err := os.WriteFile(tempBinary, input, 0755); err != nil {
		return fmt.Errorf("failed to write binary: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempBinary, destBinary); err != nil {
		os.Remove(tempBinary)
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

// SetCurrentVersion updates the current symlink to point to a version atomically
func (m *Manager) SetCurrentVersion(version string) error {
	// Verify version exists
	if !m.IsVersionInstalled(version) {
		return fmt.Errorf("version %s is not installed", version)
	}

	currentLink := m.GetCurrentLink()
	targetDir := m.GetVersionDir(version)

	// Create new symlink with temp name
	tempLink := currentLink + ".tmp"
	os.Remove(tempLink) // cleanup any leftover
	
	if err := os.Symlink(targetDir, tempLink); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempLink, currentLink); err != nil {
		os.Remove(tempLink)
		return fmt.Errorf("failed to update symlink: %w", err)
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

