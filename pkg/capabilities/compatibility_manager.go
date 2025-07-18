package capabilities

import (
	"fmt"
	"strconv"
	"strings"
)

// CompatibilityManagerImpl implements the CompatibilityManager interface
type CompatibilityManagerImpl struct{}

// NewCompatibilityManager creates a new CompatibilityManager instance
func NewCompatibilityManager() CompatibilityManager {
	return &CompatibilityManagerImpl{}
}

// CheckCompatibility compares local and peer capabilities and returns compatibility result
func (cm *CompatibilityManagerImpl) CheckCompatibility(local, peer *Capabilities) (*CompatibilityResult, error) {
	if local == nil || peer == nil {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     "nil capabilities provided",
			Details:    map[string]interface{}{"error": "local or peer capabilities is nil"},
		}, nil
	}

	// Check version compatibility
	versionCompatible, err := cm.IsVersionCompatible(local.Version, peer.Version)
	if err != nil {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     fmt.Sprintf("version compatibility check failed: %v", err),
			Details:    map[string]interface{}{"error": err.Error()},
		}, nil
	}

	if !versionCompatible {
		return &CompatibilityResult{
			Compatible: false,
			Reason:     "version incompatible",
			Details: map[string]interface{}{
				"local_version": local.Version,
				"peer_version":  peer.Version,
			},
		}, nil
	}

	return &CompatibilityResult{
		Compatible: true,
		Reason:     "compatible",
		Details: map[string]interface{}{
			"local_version": local.Version,
			"peer_version":  peer.Version,
		},
	}, nil
}

// IsVersionCompatible checks if two version strings are compatible using semantic versioning
func (cm *CompatibilityManagerImpl) IsVersionCompatible(localVersion, peerVersion string) (bool, error) {
	localVer, err := parseVersion(localVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse local version %s: %w", localVersion, err)
	}

	peerVer, err := parseVersion(peerVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse peer version %s: %w", peerVersion, err)
	}

	// Compatible if major versions match
	return localVer.Major == peerVer.Major, nil
}

// HasRequiredCapabilities checks if the given capabilities include all required actions
func (cm *CompatibilityManagerImpl) HasRequiredCapabilities(caps *Capabilities, required []string) bool {
	if caps == nil || caps.SupportedActions == nil {
		return len(required) == 0
	}

	supportedSet := make(map[string]bool)
	for _, action := range caps.SupportedActions {
		supportedSet[action] = true
	}

	for _, req := range required {
		if !supportedSet[req] {
			return false
		}
	}

	return true
}

// parseVersion parses a semantic version string into VersionInfo
func parseVersion(version string) (*VersionInfo, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid patch version: %s", parts[2])
	}

	return &VersionInfo{
		Major: major,
		Minor: minor,
		Patch: patch,
	}, nil
}