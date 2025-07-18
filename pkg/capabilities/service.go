package capabilities

import (
	"time"
)

// LoadCapabilitiesFromConfig loads capabilities from a configuration file
func LoadCapabilitiesFromConfig(configPath string) (*Capabilities, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	return &Capabilities{
		Version:          config.Version,
		SupportedActions: config.SupportedActions,
		ActionVersions:   config.ActionVersions,
		Metadata:         config.Metadata,
		Timestamp:        time.Now(),
	}, nil
}

// CreateDefaultCapabilities creates default capabilities when no config is available
func CreateDefaultCapabilities() *Capabilities {
	return &Capabilities{
		Version:          "1.0.0",
		SupportedActions: []string{"cascade"},
		ActionVersions: map[string][]string{
			"cascade": {"1.0.0"},
		},
		Metadata: map[string]string{
			"build_info": "default",
			"features":   "basic",
		},
		Timestamp: time.Now(),
	}
}