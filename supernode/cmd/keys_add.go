package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// keysAddCmd represents the add command for creating a new key
var keysAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new key",
	Long: `Add a new key with the given name.
This command will generate a new mnemonic and derive a key pair from it.
The generated key pair will be stored in the keyring.

Example:
  lumera-cli keys add mykey`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := logtrace.CtxWithCorrelationID(cmd.Context(), "keys-add")

		logtrace.Info(ctx, "Starting key add command", logtrace.Fields{
			"args_count": len(args),
		})

		var keyName string
		if len(args) > 0 {
			keyName = args[0]
		} else {
			// Use the key_name from config file as default
			keyName = appConfig.SupernodeConfig.KeyName
		}

		if keyName == "" {
			logtrace.Error(ctx, "Key name is missing", logtrace.Fields{})
			return fmt.Errorf("key name is required")
		}

		logtrace.Info(ctx, "Initializing keyring", logtrace.Fields{
			"key_name": keyName,
			"backend":  appConfig.KeyringConfig.Backend,
			"dir":      appConfig.KeyringConfig.Dir,
		})

		// Initialize keyring using config values
		kr, err := keyring.InitKeyring(
			appConfig.KeyringConfig.Backend,
			appConfig.KeyringConfig.Dir,
		)
		if err != nil {
			logtrace.Error(ctx, "Failed to initialize keyring", logtrace.Fields{
				"error": err.Error(),
			})
			return fmt.Errorf("failed to initialize keyring: %w", err)
		}

		logtrace.Info(ctx, "Generating new account", logtrace.Fields{
			"key_name": keyName,
			"entropy":  256,
		})

		// Generate mnemonic and create new account
		// Default to 256 bits of entropy (24 words)
		mnemonic, info, err := keyring.CreateNewAccount(kr, keyName, 256)
		if err != nil {
			logtrace.Error(ctx, "Failed to create new account", logtrace.Fields{
				"key_name": keyName,
				"error":    err.Error(),
			})
			return fmt.Errorf("failed to create new account: %w", err)
		}

		// Get address
		address, err := info.GetAddress()
		if err != nil {
			logtrace.Error(ctx, "Failed to get address from account info", logtrace.Fields{
				"key_name": keyName,
				"error":    err.Error(),
			})
			return fmt.Errorf("failed to get address: %w", err)
		}

		logtrace.Info(ctx, "Key generated successfully", logtrace.Fields{
			"key_name": info.Name,
			"address":  address.String(),
		})

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
