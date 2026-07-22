package version

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/LumeraProtocol/supernode/v2/pkg/configlock"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
)

var ErrNoCurrentVersion = errors.New("no version currently set")

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

// AcquireInstallLock serializes release download, extraction, and installation
// across sn-manager processes that share this home directory.
func (m *Manager) AcquireInstallLock() (func() error, error) {
	return configlock.Acquire(filepath.Join(m.homeDir, "install"))
}

// InstallVersion installs a binary to the version directory atomically
func (m *Manager) InstallVersion(version string, binaryPath string) error {
	if err := os.MkdirAll(m.GetBinariesDir(), 0o755); err != nil {
		return fmt.Errorf("failed to create binaries directory: %w", err)
	}
	release, err := configlock.Acquire(filepath.Join(m.GetBinariesDir(), "."+version))
	if err != nil {
		return fmt.Errorf("failed to lock version installation: %w", err)
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			log.Printf("Warning: failed to release version install lock: %v", releaseErr)
		}
	}()

	// Create version directory
	versionDir := m.GetVersionDir(version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}

	// Destination binary path
	destBinary := m.GetVersionBinary(version)
	tempBinary := destBinary + ".tmp"

	// Stream copy binary to temp location first to avoid high memory usage
	src, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("failed to open binary: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(tempBinary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temp binary: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		if rmErr := os.Remove(tempBinary); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("Warning: failed to cleanup temp binary after copy error: %v", rmErr)
		}
		return fmt.Errorf("failed to copy binary: %w", err)
	}
	if err := dst.Close(); err != nil {
		if rmErr := os.Remove(tempBinary); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("Warning: failed to cleanup temp binary after close error: %v", rmErr)
		}
		return fmt.Errorf("failed to close temp binary: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempBinary, destBinary); err != nil {
		if rmErr := os.Remove(tempBinary); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("Warning: failed to cleanup temp binary after rename error: %v", rmErr)
		}
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

// SetCurrentVersion updates the current symlink to point to a version atomically.
// It is operator-explicit and therefore permits selecting any installed version.
func (m *Manager) SetCurrentVersion(version string) error {
	release, err := configlock.Acquire(m.GetCurrentLink())
	if err != nil {
		return err
	}
	if err := m.setCurrentVersionUnlocked(version); err != nil {
		_ = release()
		return err
	}
	return release()
}

// SetCurrentVersionIfNewer atomically compares the active symlink and advances
// it only when version has higher semantic precedence. Automatic update paths
// use this method so a concurrent explicit `use` cannot turn into a downgrade.
func (m *Manager) SetCurrentVersionIfNewer(version string) (bool, error) {
	release, err := configlock.Acquire(m.GetCurrentLink())
	if err != nil {
		return false, err
	}
	defer func() { _ = release() }()

	current, err := m.GetCurrentVersion()
	if err != nil {
		if !errors.Is(err, ErrNoCurrentVersion) {
			return false, err
		}
		if err := m.setCurrentVersionUnlocked(version); err != nil {
			return false, err
		}
		return true, nil
	}
	if utils.CompareVersions(current, version) >= 0 {
		return false, nil
	}
	if err := m.setCurrentVersionUnlocked(version); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) setCurrentVersionUnlocked(version string) error {
	// Verify version exists
	if !m.IsVersionInstalled(version) {
		return fmt.Errorf("version %s is not installed", version)
	}

	currentLink := m.GetCurrentLink()
	targetDir := m.GetVersionDir(version)

	// Create new symlink with temp name
	tempLink := currentLink + ".tmp"
	if err := os.Remove(tempLink); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove existing temp link: %v", err)
	}

	if err := os.Symlink(targetDir, tempLink); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempLink, currentLink); err != nil {
		if rmErr := os.Remove(tempLink); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("Warning: failed to cleanup temp link: %v", rmErr)
		}
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
			return "", ErrNoCurrentVersion
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
			versions = append(versions, entry.Name())
		}
	}

	// Sort versions (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return utils.CompareVersions(versions[i], versions[j]) > 0
	})

	return versions, nil
}
