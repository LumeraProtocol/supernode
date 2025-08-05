package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize sn-manager environment",
	Long: `Initialize the sn-manager environment and configuration.

This command:
1. Creates ~/.sn-manager directory structure
2. Generates sn-manager configuration file
3. Sets up directories for version management

Note: To initialize SuperNode itself, use 'sn-manager init-supernode' or 'supernode init' directly.`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	fmt.Println("Initializing sn-manager...")
	
	// Determine home directory
	home := homeDir
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		home = filepath.Join(userHome, ".sn-manager")
	}

	// Check if already initialized
	configPath := filepath.Join(home, "config.yml")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("sn-manager already initialized at %s\n", home)
		fmt.Println("To reinitialize, please remove the directory first.")
		return nil
	}

	// Create directory structure
	dirs := []string{
		home,
		filepath.Join(home, "binaries"),
		filepath.Join(home, "downloads"),
		filepath.Join(home, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create default config
	cfg := config.DefaultConfig()
	
	// Set supernode home directory to default location
	userHome, _ := os.UserHomeDir()
	cfg.SuperNode.Home = filepath.Join(userHome, ".supernode")
	
	// Save config
	if err := config.Save(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("\nsn-manager initialized successfully at %s\n", home)
	fmt.Printf("\nConfiguration saved to: %s\n", configPath)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("1. Initialize SuperNode (if not already done):\n")
	fmt.Printf("   sn-manager init-supernode\n")
	fmt.Printf("2. Download SuperNode binary:\n")
	fmt.Printf("   sn-manager upgrade\n")
	fmt.Printf("3. Start SuperNode:\n")
	fmt.Printf("   sn-manager start\n")

	return nil
}