package cmd

import (
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list [service]",
	Short: "List available gRPC services or methods in a specific service",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			return app.ListGRPCMethods(args[0])
		}
		return app.ListGRPCMethods("")
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}