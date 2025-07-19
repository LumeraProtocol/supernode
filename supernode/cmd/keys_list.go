package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// keysListCmd represents the list command for listing all keys
var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all keys in the keyring",
	Long: `List all keys stored in the keyring with their addresses.
This command displays a table with key names, types, and addresses.

Example:
  supernode keys list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Initialize keyring using helper function
		kr, err := initKeyringFromConfig(appConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize keyring: %w", err)
		}

		// Get all keys from keyring
		keyInfos, err := kr.List()
		if err != nil {
			return fmt.Errorf("failed to list keys: %w", err)
		}

		// Sort keys by name for consistent output
		sort.Slice(keyInfos, func(i, j int) bool {
			return keyInfos[i].Name < keyInfos[j].Name
		})

		// Check if we found any keys
		if len(keyInfos) == 0 {
			fmt.Println("No keys found in keyring")
			return nil
		}

		// Format output with tabwriter for aligned columns
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tADDRESS")
		fmt.Fprintln(w, "----\t-------")

		// Print key information
		for _, info := range keyInfos {
			// Get address from key info
			address, err := info.GetAddress()
			if err != nil {
				return fmt.Errorf("failed to get address for key %s: %w", info.Name, err)
			}

			// Check if this key is selected in config
			name := info.Name
			if name == appConfig.SupernodeConfig.KeyName && address.String() == appConfig.SupernodeConfig.Identity {
				name = name + " (selected)"
			}

			fmt.Fprintf(w, "%s\t%s\n", name, address.String())
		}
		w.Flush()

		return nil
	},
}

func init() {
	keysCmd.AddCommand(keysListCmd)
}
