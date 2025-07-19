package cmd

import (
	"github.com/spf13/cobra"
)

// keysCmd represents the keys command
var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage keys",
	Long: `Manage keys for the Supernode.
This command provides subcommands for recovering and listing keys.`,
}

func init() {
	rootCmd.AddCommand(keysCmd)
}
