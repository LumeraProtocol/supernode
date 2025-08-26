package cmd

import (
	"github.com/spf13/cobra"
	"github.com/LumeraProtocol/supernode/v2/cmd/sncli/cli"
)

var p2pCmd = &cobra.Command{
	Use:   "p2p",
	Short: "Supernode P2P utilities",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
        if r := cmd.Root(); r != nil && r.PersistentPreRunE != nil {
            if err := r.PersistentPreRunE(cmd, args); err != nil {
                return err
            }
        }
		if app.P2P() == nil {
			p := &cli.P2P{}
			if err := p.Initialize(app); err != nil {
				return err
			}
			app.SetP2P(p)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(p2pCmd)
}
