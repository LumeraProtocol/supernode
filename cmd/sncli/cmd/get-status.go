package cmd

import (
	"github.com/spf13/cobra"
)

// get-status: Query Supernode's current status (CPU, memory, tasks, peers, etc.)
var statusCmd = &cobra.Command{
	Use:     "get-status",
	Aliases: []string{"status"},
	Short:   "Query Supernode status (CPU, memory, tasks, peers)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.GetSupernodeStatus()
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
