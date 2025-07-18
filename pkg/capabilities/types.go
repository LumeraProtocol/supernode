package capabilities

import (
	"time"
)

// Capabilities represents the runtime capabilities of a supernode
type Capabilities struct {
	Version          string            `json:"version"`
	SupportedActions []string          `json:"supported_actions"`
	ActionVersions   map[string][]string `json:"action_versions"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Timestamp        time.Time         `json:"timestamp"`
}

// CapabilityConfig represents the YAML configuration structure for capabilities
type CapabilityConfig struct {
	Version          string              `yaml:"version"`
	SupportedActions []string            `yaml:"supported_actions"`
	ActionVersions   map[string][]string `yaml:"action_versions"`
	Metadata         map[string]string   `yaml:"metadata,omitempty"`
}

// CompatibilityResult represents the result of a compatibility check between two supernodes
type CompatibilityResult struct {
	Compatible bool                   `json:"compatible"`
	Reason     string                 `json:"reason"`
	Details    map[string]interface{} `json:"details"`
}

// VersionInfo represents parsed semantic version information
type VersionInfo struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}