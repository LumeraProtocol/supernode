package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
)

func checkInitialized() error {
	homeDir := config.GetManagerHome()
	configPath := filepath.Join(homeDir, "config.yml")
	
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("not initialized. Run: sn-manager init")
	}
	
	return nil
}

func loadConfig() (*config.Config, error) {
	homeDir := config.GetManagerHome()
	configPath := filepath.Join(homeDir, "config.yml")
	
	return config.Load(configPath)
}

func getHomeDir() string {
	return config.GetManagerHome()
}