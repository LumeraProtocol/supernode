package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
)

// keysAddCmd represents the add command for creating a new key
var keysAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new key",
	Long: `Add a new key with the given name.
This command will generate a new mnemonic and derive a key pair from it.
The generated key pair will be stored in the keyring.

Example:
  supernode keys add mykey`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var keyName string
		if len(args) > 0 {
			keyName = args[0]
		} else {
			// Use the key_name from config file as default
			keyName = appConfig.SupernodeConfig.KeyName
		}

		if keyName == "" {
			return fmt.Errorf("key name is required")
		}

		// Initialize keyring using config values
		kr, err := keyring.InitKeyring(
			appConfig.KeyringConfig.Backend,
			appConfig.GetKeyringDir(),
		)
		if err != nil {
			return fmt.Errorf("failed to initialize keyring: %w", err)
		}

		// Generate mnemonic and create new account
		// Default to 256 bits of entropy (24 words)
		mnemonic, info, err := keyring.CreateNewAccount(kr, keyName, 256)
		if err != nil {
			return fmt.Errorf("failed to create new account: %w", err)
		}

		// Get address
		address, err := info.GetAddress()
		if err != nil {
			return fmt.Errorf("failed to get address: %w", err)
		}

		// Print results
		fmt.Println("Key generated successfully!")
		fmt.Printf("- Name: %s\n", info.Name)
		fmt.Printf("- Address: %s\n", address.String())
		fmt.Printf("- Mnemonic: %s\n", mnemonic)
		fmt.Println("\nIMPORTANT: Write down the mnemonic and keep it in a safe place.")
		fmt.Println("The mnemonic is the only way to recover your account if you forget your password.")

		return nil
	},
}

func init() {
	keysCmd.AddCommand(keysAddCmd)
}
