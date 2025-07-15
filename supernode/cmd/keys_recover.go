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
Supports standard BIP39 mnemonic lengths: 12, 15, 18, 21, or 24 words.

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

		// Initialize keyring using helper function
		kr, err := initKeyringFromConfig(appConfig)
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
		}

		// Process and validate mnemonic using helper function
		processedMnemonic, err := processAndValidateMnemonic(mnemonic)
		if err != nil {
			return err
		}

		// Add debug output to see what's being processed
		wordCount := len(strings.Fields(processedMnemonic))
		fmt.Printf("Processing mnemonic with %d words\n", wordCount)

		// Recover account from mnemonic
		info, err := keyring.RecoverAccountFromMnemonic(kr, keyName, processedMnemonic)
		if err != nil {
			// Check if the error is due to an invalid mnemonic
			return fmt.Errorf("failed to recover account: %w", err)
		}

		// Get address using helper function
		address, err := getAddressFromKeyName(kr, keyName)
		if err != nil {
			return fmt.Errorf("failed to get address: %w", err)
		}

		// Print results
		fmt.Println("Key recovered successfully!")
		fmt.Printf("- Name: %s\n", info.Name)
		fmt.Printf("- Address: %s\n", address.String())
		fmt.Printf("- Mnemonic length: %d words\n", wordCount)

		return nil
	},
}

func init() {
	keysCmd.AddCommand(keysRecoverCmd)
	// Add flag for mnemonic
	keysRecoverCmd.Flags().String("mnemonic", "", "BIP39 mnemonic for key recovery (12, 15, 18, 21, or 24 words)")
}
