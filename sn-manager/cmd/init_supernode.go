package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initSupernodeCmd = &cobra.Command{
	Use:   "init-supernode",
	Short: "Initialize SuperNode configuration",
	Long: `Initialize SuperNode by relaying the init command to the supernode binary.

All flags and arguments are passed directly to 'supernode init'.
This allows full compatibility with supernode's initialization options.`,
	DisableFlagParsing: true, // Pass all flags to supernode
	RunE:               runInitSupernode,
}

func runInitSupernode(cmd *cobra.Command, args []string) error {
	home := getHomeDir()
	
	// Check if sn-manager is initialized
	configPath := filepath.Join(home, "config.yml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("sn-manager not initialized. Run 'sn-manager init' first")
	}
	
	// Get the managed supernode binary path
	supernodeBinary := filepath.Join(home, "current", "supernode")
	
	// Check if supernode binary exists
	if _, err := os.Stat(supernodeBinary); os.IsNotExist(err) {
		return fmt.Errorf("supernode binary not found. Run 'sn-manager install' first to download supernode")
	}
	
	fmt.Println("Initializing SuperNode...")

	// Build the supernode command with all passed arguments
	supernodeCmd := exec.Command(supernodeBinary, append([]string{"init"}, args...)...)
	supernodeCmd.Stdout = os.Stdout
	supernodeCmd.Stderr = os.Stderr
	supernodeCmd.Stdin = os.Stdin

	// Run supernode init
	if err := supernodeCmd.Run(); err != nil {
		return fmt.Errorf("supernode init failed: %w", err)
	}

	fmt.Println("\nSuperNode initialized successfully!")
	fmt.Println("You can now start SuperNode with: sn-manager start")

	return nil
}
