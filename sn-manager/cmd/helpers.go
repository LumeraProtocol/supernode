package cmd

import (
	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
)

// getHomeDir returns the sn-manager home directory
func getHomeDir() string {
	return config.GetManagerHome()
}
