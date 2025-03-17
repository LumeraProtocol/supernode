package cmd

import (
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/spf13/cobra"
)

// keysCmd represents the keys command
var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage keys",
	Long: `Manage keys for the Lumera blockchain.
This command provides subcommands for adding, recovering, and listing keys.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Create a context with correlation ID that will be inherited by subcommands
		ctx := logtrace.CtxWithCorrelationID(cmd.Context(), "keys-command")
		cmd.SetContext(ctx)

		logtrace.Info(ctx, "Keys command invoked", logtrace.Fields{
			"subcommand": cmd.Name(),
		})

		// Execute the parent's PersistentPreRunE to ensure config is loaded
		if parent := cmd.Parent(); parent != nil && parent.PersistentPreRunE != nil {
			return parent.PersistentPreRunE(cmd, args)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(keysCmd)
}
