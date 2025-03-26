package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
)

// keysRecoverCmd represents the recover command for recovering a key from mnemonic
var keysRecoverCmd = &cobra.Command{
	Use:   "recover [name]",
	Short: "Recover a key using a mnemonic",
	Long: `Recover a key using a BIP39 mnemonic.
This command will derive a key pair from the provided mnemonic and store it in the keyring.

Example:
  supernode keys recover mykey`,
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
			appConfig.KeyringConfig.Dir,
		)
		if err != nil {
			return fmt.Errorf("failed to initialize keyring: %w", err)
		}

		// Prompt for mnemonic or use from config
		var mnemonic string

		fmt.Print("Enter your mnemonic: ")
		reader := bufio.NewReader(os.Stdin)
		mnemonic, err = reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read mnemonic: %w", err)
		}
		mnemonic = strings.TrimSpace(mnemonic)

		// Recover account from mnemonic
		info, err := keyring.RecoverAccountFromMnemonic(kr, keyName, mnemonic)
		if err != nil {
			return fmt.Errorf("failed to recover account: %w", err)
		}

		// Get address
		address, err := info.GetAddress()
		if err != nil {
			return fmt.Errorf("failed to get address: %w", err)
		}

		// Print results
		fmt.Println("Key recovered successfully!")
		fmt.Printf("- Name: %s\n", info.Name)
		fmt.Printf("- Address: %s\n", address.String())

		return nil
	},
}

func init() {
	keysCmd.AddCommand(keysRecoverCmd)
	// Remove all flags - we'll use config file only
}
