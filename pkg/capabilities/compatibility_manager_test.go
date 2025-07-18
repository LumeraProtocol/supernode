package capabilities

import (
	"testing"
	"time"
)

func TestCompatibilityManager_CheckCompatibility(t *testing.T) {
	cm := NewCompatibilityManager()

	tests := []struct {
		name     string
		local    *Capabilities
		peer     *Capabilities
		expected *CompatibilityResult
		wantErr  bool
	}{
		{
			name:  "nil_capabilities",
			local: nil,
			peer:  nil,
			expected: &CompatibilityResult{
				Compatible: false,
				Reason:     "nil capabilities provided",
			},
			wantErr: false,
		},
		{
			name: "compatible_same_version",
			local: &Capabilities{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			peer: &Capabilities{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			expected: &CompatibilityResult{
				Compatible: true,
				Reason:     "compatible",
			},
			wantErr: false,
		},
		{
			name: "compatible_different_minor",
			local: &Capabilities{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			peer: &Capabilities{
				Version:          "1.1.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			expected: &CompatibilityResult{
				Compatible: true,
				Reason:     "compatible",
			},
			wantErr: false,
		},
		{
			name: "incompatible_different_major",
			local: &Capabilities{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			peer: &Capabilities{
				Version:          "2.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			expected: &CompatibilityResult{
				Compatible: false,
				Reason:     "version incompatible",
			},
			wantErr: false,
		},
		{
			name: "invalid_version_format",
			local: &Capabilities{
				Version:          "invalid",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			peer: &Capabilities{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				Timestamp:        time.Now(),
			},
			expected: &CompatibilityResult{
				Compatible: false,
				Reason:     "version compatibility check failed: failed to parse local version invalid: invalid version format: invalid",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := cm.CheckCompatibility(tt.local, tt.peer)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckCompatibility() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if result.Compatible != tt.expected.Compatible {
				t.Errorf("CheckCompatibility() Compatible = %v, expected %v", result.Compatible, tt.expected.Compatible)
			}

			if result.Reason != tt.expected.Reason {
				t.Errorf("CheckCompatibility() Reason = %v, expected %v", result.Reason, tt.expected.Reason)
			}
		})
	}
}

func TestCompatibilityManager_IsVersionCompatible(t *testing.T) {
	cm := NewCompatibilityManager()

	tests := []struct {
		name         string
		localVersion string
		peerVersion  string
		expected     bool
		wantErr      bool
	}{
		{
			name:         "same_version",
			localVersion: "1.0.0",
			peerVersion:  "1.0.0",
			expected:     true,
			wantErr:      false,
		},
		{
			name:         "same_major_different_minor",
			localVersion: "1.0.0",
			peerVersion:  "1.1.0",
			expected:     true,
			wantErr:      false,
		},
		{
			name:         "different_major",
			localVersion: "1.0.0",
			peerVersion:  "2.0.0",
			expected:     false,
			wantErr:      false,
		},
		{
			name:         "invalid_local_version",
			localVersion: "invalid",
			peerVersion:  "1.0.0",
			expected:     false,
			wantErr:      true,
		},
		{
			name:         "invalid_peer_version",
			localVersion: "1.0.0",
			peerVersion:  "invalid",
			expected:     false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := cm.IsVersionCompatible(tt.localVersion, tt.peerVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsVersionCompatible() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if result != tt.expected {
				t.Errorf("IsVersionCompatible() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestCompatibilityManager_HasRequiredCapabilities(t *testing.T) {
	cm := NewCompatibilityManager()

	tests := []struct {
		name     string
		caps     *Capabilities
		required []string
		expected bool
	}{
		{
			name:     "nil_capabilities_no_requirements",
			caps:     nil,
			required: []string{},
			expected: true,
		},
		{
			name:     "nil_capabilities_with_requirements",
			caps:     nil,
			required: []string{"cascade"},
			expected: false,
		},
		{
			name: "has_all_required",
			caps: &Capabilities{
				SupportedActions: []string{"cascade", "sense", "storage_challenge"},
			},
			required: []string{"cascade", "sense"},
			expected: true,
		},
		{
			name: "missing_required",
			caps: &Capabilities{
				SupportedActions: []string{"cascade"},
			},
			required: []string{"cascade", "sense"},
			expected: false,
		},
		{
			name: "no_requirements",
			caps: &Capabilities{
				SupportedActions: []string{"cascade"},
			},
			required: []string{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cm.HasRequiredCapabilities(tt.caps, tt.required)
			if result != tt.expected {
				t.Errorf("HasRequiredCapabilities() = %v, expected %v", result, tt.expected)
			}
		})
	}
}