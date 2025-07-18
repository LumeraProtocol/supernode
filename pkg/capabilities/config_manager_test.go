package capabilities

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigManager_LoadConfig(t *testing.T) {

	tests := []struct {
		name           string
		configContent  string
		expectedConfig *CapabilityConfig
		wantErr        bool
	}{
		{
			name: "load_nonexistent_file",
			expectedConfig: &CapabilityConfig{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				ActionVersions: map[string][]string{
					"cascade": {"1.0.0"},
				},
				Metadata: map[string]string{
					"build_info": "default",
					"features":   "basic",
				},
			},
			wantErr: false,
		},
		{
			name: "load_valid_yaml",
			configContent: `version: "2.1.0"
supported_actions:
  - cascade
  - sense
action_versions:
  cascade:
    - "2.0.0"
    - "2.1.0"
  sense:
    - "1.0.0"
metadata:
  build_info: "test"
  features: "advanced"`,
			expectedConfig: &CapabilityConfig{
				Version:          "2.1.0",
				SupportedActions: []string{"cascade", "sense"},
				ActionVersions: map[string][]string{
					"cascade": {"2.0.0", "2.1.0"},
					"sense":   {"1.0.0"},
				},
				Metadata: map[string]string{
					"build_info": "test",
					"features":   "advanced",
				},
			},
			wantErr: false,
		},
		{
			name: "load_minimal_config",
			configContent: `version: "1.0.0"
supported_actions:
  - cascade
action_versions:
  cascade:
    - "1.0.0"`,
			expectedConfig: &CapabilityConfig{
				Version:          "1.0.0",
				SupportedActions: []string{"cascade"},
				ActionVersions: map[string][]string{
					"cascade": {"1.0.0"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configPath string
			
			if tt.configContent != "" {
				tempDir := t.TempDir()
				configPath = filepath.Join(tempDir, "test_config.yaml")
				err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
				if err != nil {
					t.Fatalf("Failed to write test config file: %v", err)
				}
			} else {
				configPath = "/non/existent/file.yaml"
			}

			config, err := LoadConfig(configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if config == nil {
				t.Fatal("LoadConfig() returned nil config")
			}

			// Verify version
			if config.Version != tt.expectedConfig.Version {
				t.Errorf("Expected version '%s', got '%s'", tt.expectedConfig.Version, config.Version)
			}

			// Verify supported actions
			if len(config.SupportedActions) != len(tt.expectedConfig.SupportedActions) {
				t.Errorf("Expected %d supported actions, got %d", len(tt.expectedConfig.SupportedActions), len(config.SupportedActions))
			} else {
				for i, expected := range tt.expectedConfig.SupportedActions {
					if config.SupportedActions[i] != expected {
						t.Errorf("Expected action '%s' at index %d, got '%s'", expected, i, config.SupportedActions[i])
					}
				}
			}

			// Verify action versions
			if len(config.ActionVersions) != len(tt.expectedConfig.ActionVersions) {
				t.Errorf("Expected %d action versions, got %d", len(tt.expectedConfig.ActionVersions), len(config.ActionVersions))
			} else {
				for action, expectedVersions := range tt.expectedConfig.ActionVersions {
					actualVersions, exists := config.ActionVersions[action]
					if !exists {
						t.Errorf("Expected action versions for '%s' to exist", action)
						continue
					}
					if len(actualVersions) != len(expectedVersions) {
						t.Errorf("Expected %d versions for action '%s', got %d", len(expectedVersions), action, len(actualVersions))
					} else {
						for i, expected := range expectedVersions {
							if actualVersions[i] != expected {
								t.Errorf("Expected version '%s' for action '%s' at index %d, got '%s'", expected, action, i, actualVersions[i])
							}
						}
					}
				}
			}

			// Verify metadata if expected
			if tt.expectedConfig.Metadata != nil {
				if config.Metadata == nil {
					t.Error("Expected metadata to exist")
				} else {
					for key, expectedValue := range tt.expectedConfig.Metadata {
						actualValue, exists := config.Metadata[key]
						if !exists {
							t.Errorf("Expected metadata key '%s' to exist", key)
						} else if actualValue != expectedValue {
							t.Errorf("Expected metadata '%s' = '%s', got '%s'", key, expectedValue, actualValue)
						}
					}
				}
			}
		})
	}
}