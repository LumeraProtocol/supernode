package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// configListCmd represents the config list command
var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Display current configuration",
	Long:  `Display the current configuration parameters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Format output with tabwriter for aligned columns
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PARAMETER\tVALUE")
		fmt.Fprintln(w, "---------\t-------------")

		// Display configuration parameters
		fmt.Fprintf(w, "Key Name\t%s\n", appConfig.SupernodeConfig.KeyName)
		fmt.Fprintf(w, "Address\t%s\n", appConfig.SupernodeConfig.Identity)
		fmt.Fprintf(w, "Supernode Address\t%s\n", appConfig.SupernodeConfig.IpAddress)
		fmt.Fprintf(w, "Supernode Port\t%d\n", appConfig.SupernodeConfig.Port)
		fmt.Fprintf(w, "Keyring Backend\t%s\n", appConfig.KeyringConfig.Backend)
		fmt.Fprintf(w, "Lumera GRPC Address\t%s\n", appConfig.LumeraClientConfig.GRPCAddr)
		fmt.Fprintf(w, "Chain ID\t%s\n", appConfig.LumeraClientConfig.ChainID)

		w.Flush()
		return nil
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
}
