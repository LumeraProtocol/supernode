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
  supernode keys recover mykey
  supernode keys recover mykey --mnemonic="your mnemonic words here"`,
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

		// Get mnemonic from flag or prompt
		mnemonic, err := cmd.Flags().GetString("mnemonic")
		if err != nil {
			return fmt.Errorf("failed to get mnemonic flag: %w", err)
		}

		// If mnemonic wasn't provided as a flag, prompt for it
		if mnemonic == "" {
			fmt.Print("Enter your mnemonic: ")
			reader := bufio.NewReader(os.Stdin)
			mnemonic, err = reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read mnemonic: %w", err)
			}
			mnemonic = strings.TrimSpace(mnemonic)
		}

		// Process the mnemonic to ensure proper formatting
		mnemonic = strings.TrimSpace(mnemonic)
		// Normalize whitespace (replace multiple spaces with single space)
		mnemonic = strings.Join(strings.Fields(mnemonic), " ")

		// Add debug output to see what's being processed
		fmt.Printf("Processing mnemonic with %d words\n", len(strings.Fields(mnemonic)))

		// Check expected word count (BIP39 mnemonics are typically 12 or 24 words)
		wordCount := len(strings.Fields(mnemonic))
		if wordCount != 24 {
			return fmt.Errorf("mnemonic should have 24 words exactly, found %d words", wordCount)
		}

		// Recover account from mnemonic
		info, err := keyring.RecoverAccountFromMnemonic(kr, keyName, mnemonic)
		if err != nil {
			// Check if the error is due to an invalid mnemonic
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
	// Add flag for mnemonic
	keysRecoverCmd.Flags().String("mnemonic", "", "BIP39 mnemonic for key recovery")
}
