package cmd

import (
	"github.com/spf13/cobra"
)

// keysCmd represents the keys command
var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage keys",
	Long: `Manage keys for the Lumera blockchain.
This command provides subcommands for adding, recovering, and listing keys.`,
}

func init() {
	rootCmd.AddCommand(keysCmd)
}
