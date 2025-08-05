package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/manager"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the managed SuperNode",
	Long:  `Stop and restart the SuperNode process.`,
	RunE:  runRestart,
}

func runRestart(cmd *cobra.Command, args []string) error {
	fmt.Println("Restarting SuperNode...")
	
	// First stop the SuperNode
	if err := runStop(cmd, args); err != nil {
		return fmt.Errorf("failed to stop SuperNode: %w", err)
	}
	
	// Wait a moment to ensure clean shutdown
	time.Sleep(1 * time.Second)
	
	// Determine home directory
	home := homeDir
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		home = filepath.Join(userHome, ".sn-manager")
	}

	// Check if initialized
	configPath := filepath.Join(home, "config.yml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("sn-manager not initialized. Run 'sn-manager init' first")
	}

	// Create manager instance
	mgr, err := manager.New(home)
	if err != nil {
		return fmt.Errorf("failed to create manager: %w", err)
	}

	// Start SuperNode
	ctx := context.Background()
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start supernode: %w", err)
	}

	fmt.Println("SuperNode restarted successfully")
	return nil
}