package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var initSupernodeCmd = &cobra.Command{
	Use:   "init-supernode",
	Short: "Initialize SuperNode configuration",
	Long: `Initialize SuperNode by relaying the init command to the supernode binary.

All flags and arguments are passed directly to 'supernode init'.
This allows full compatibility with supernode's initialization options.`,
	DisableFlagParsing: true, // Pass all flags to supernode
	RunE: runInitSupernode,
}

func runInitSupernode(cmd *cobra.Command, args []string) error {
	fmt.Println("Initializing SuperNode...")
	
	// Build the supernode command with all passed arguments
	supernodeCmd := exec.Command("supernode", append([]string{"init"}, args...)...)
	supernodeCmd.Stdout = os.Stdout
	supernodeCmd.Stderr = os.Stderr
	supernodeCmd.Stdin = os.Stdin
	
	// Run supernode init
	if err := supernodeCmd.Run(); err != nil {
		return fmt.Errorf("supernode init failed: %w", err)
	}
	
	fmt.Println("\nSuperNode initialized successfully!")
	fmt.Println("\nNext, initialize sn-manager with: sn-manager init")
	
	return nil
}