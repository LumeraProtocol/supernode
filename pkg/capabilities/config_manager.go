package capabilities

import (
	"fmt"

	"github.com/spf13/viper"
)

func LoadConfig(path string) (*CapabilityConfig, error) {

	// Configure viper to read the YAML file
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Read the configuration file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	// Unmarshal into CapabilityConfig struct
	var config CapabilityConfig
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}
