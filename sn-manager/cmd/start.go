package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/LumeraProtocol/supernode/sn-manager/internal/manager"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start SuperNode under management",
	Long: `Start the SuperNode process under sn-manager supervision.

The manager will:
- Launch the SuperNode process
- Monitor its health
- Restart on crashes (up to max_restart_attempts)
- Check for updates periodically
- Perform automatic upgrades if configured`,
	RunE: runStart,
}

func runStart(cmd *cobra.Command, args []string) error {
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

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start SuperNode
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start supernode: %w", err)
	}

	fmt.Println("SuperNode manager started. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down...")

	// Stop SuperNode
	if err := mgr.Stop(); err != nil {
		return fmt.Errorf("failed to stop supernode: %w", err)
	}

	return nil
}