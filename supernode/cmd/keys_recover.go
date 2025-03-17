package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// keysRecoverCmd represents the recover command for recovering a key from mnemonic
var keysRecoverCmd = &cobra.Command{
	Use:   "recover [name]",
	Short: "Recover a key using a mnemonic",
	Long: `Recover a key using a BIP39 mnemonic.
This command will derive a key pair from the provided mnemonic and store it in the keyring.

Example:
  lumera-cli keys recover mykey`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := logtrace.CtxWithCorrelationID(cmd.Context(), "keys-recover")

		logtrace.Info(ctx, "Starting key recovery command", logtrace.Fields{
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

		// Prompt for mnemonic or use from config
		var mnemonic string

		logtrace.Info(ctx, "Prompting user for mnemonic input", logtrace.Fields{})
		fmt.Print("Enter your mnemonic: ")
		reader := bufio.NewReader(os.Stdin)
		mnemonic, err = reader.ReadString('\n')
		if err != nil {
			logtrace.Error(ctx, "Failed to read mnemonic from user input", logtrace.Fields{
				"error": err.Error(),
			})
			return fmt.Errorf("failed to read mnemonic: %w", err)
		}
		mnemonic = strings.TrimSpace(mnemonic)

		logtrace.Info(ctx, "Recovering account from mnemonic", logtrace.Fields{
			"key_name": keyName,
		})

		// Recover account from mnemonic
		info, err := keyring.RecoverAccountFromMnemonic(kr, keyName, mnemonic)
		if err != nil {
			logtrace.Error(ctx, "Failed to recover account from mnemonic", logtrace.Fields{
				"key_name": keyName,
				"error":    err.Error(),
			})
			return fmt.Errorf("failed to recover account: %w", err)
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

		logtrace.Info(ctx, "Key recovered successfully", logtrace.Fields{
			"key_name": info.Name,
			"address":  address.String(),
		})

		// Print results
		fmt.Println("Key recovered successfully!")
		fmt.Printf("- Name: %s\n", info.Name)
		fmt.Printf("- Address: %s\n", address.String())

		return nil
	},
}

func init() {
	keysCmd.AddCommand(keysRecoverCmd)
}
