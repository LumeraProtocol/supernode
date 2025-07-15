package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/spf13/cobra"
)

var (
	initKeyName        string
	initRecover        bool
	initMnemonic       string
	initKeyringBackend string
	initKeyringDir     string
	initChainID        string
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init <key-name>",
	Short: "Initialize a new supernode",
	Long: `Initialize a new supernode by creating a configuration file and setting up keys.

This command will:
1. Create a config.yml file at the default location (~/.supernode) or at a location specified with -d
2. Create a new key or recover an existing one
3. Update the config.yml file with the key's address
4. Output the key information

Example:
  supernode init mykey --chain-id lumera
  supernode init mykey --recover --mnemonic "your mnemonic words here" --chain-id lumera
  supernode init mykey --keyring-backend file --chain-id lumera`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get key name from positional argument
		initKeyName = args[0]

		// Validate required parameters
		if initChainID == "" {
			return fmt.Errorf("chain ID is required (--chain-id)")
		}

		// Setup base directory
		var err error
		if baseDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			baseDir = filepath.Join(homeDir, DefaultBaseDir)
		}

		// Create base directory if it doesn't exist
		if err := os.MkdirAll(baseDir, 0700); err != nil {
			return fmt.Errorf("failed to create base directory: %w", err)
		}

		// Set config file path
		if cfgFile == "" {
			cfgFile = filepath.Join(baseDir, DefaultConfigFile)
		}

		fmt.Printf("Using base directory: %s\n", baseDir)
		fmt.Printf("Using config file: %s\n", cfgFile)

		// Create default configuration
		appConfig = config.CreateDefaultConfig(initKeyName, "", initChainID, initKeyringBackend, initKeyringDir)
		appConfig.BaseDir = baseDir

		// Create directories
		if err := appConfig.EnsureDirs(); err != nil {
			return fmt.Errorf("failed to create directories: %w", err)
		}

		kr, err := initKeyringFromConfig(appConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize keyring: %w", err)
		}

		var address string

		// Create or recover key
		if initRecover {
			// Recover key from mnemonic
			if initMnemonic == "" {
				return fmt.Errorf("mnemonic is required when --recover is specified")
			}

			// Process and validate mnemonic using helper function
			processedMnemonic, err := processAndValidateMnemonic(initMnemonic)
			if err != nil {
				fmt.Printf("Warning: %v\n", err)
				// Continue with original mnemonic if validation fails
				processedMnemonic = initMnemonic
			}

			info, err := keyring.RecoverAccountFromMnemonic(kr, initKeyName, processedMnemonic)
			if err != nil {
				return fmt.Errorf("failed to recover account: %w", err)
			}

			addr, err := getAddressFromKeyName(kr, initKeyName)
			if err != nil {
				return fmt.Errorf("failed to get address: %w", err)
			}
			address = addr.String()

			fmt.Println("Key recovered successfully!")
			fmt.Printf("- Name: %s\n", info.Name)
			fmt.Printf("- Address: %s\n", address)
		} else {
			// Generate mnemonic and create new account
			var mnemonic string
			mnemonic, info, err := keyring.CreateNewAccount(kr, initKeyName)
			if err != nil {
				return fmt.Errorf("failed to create new account: %w", err)
			}

			addr, err := getAddressFromKeyName(kr, initKeyName)
			if err != nil {
				return fmt.Errorf("failed to get address: %w", err)
			}
			address = addr.String()

			fmt.Println("Key generated successfully!")
			fmt.Printf("- Name: %s\n", info.Name)
			fmt.Printf("- Address: %s\n", address)
			fmt.Printf("- Mnemonic: %s\n", mnemonic)
			fmt.Println("\nIMPORTANT: Write down the mnemonic and keep it in a safe place.")
			fmt.Println("The mnemonic is the only way to recover your account if you forget your password.")
		}

		// Update config with address
		appConfig.SupernodeConfig.Identity = address

		// Save config
		if err := config.SaveConfig(appConfig, cfgFile); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("\nConfiguration saved to %s\n", cfgFile)
		fmt.Println("\nYour supernode has been initialized successfully!")
		fmt.Println("You can now start your supernode with:")
		fmt.Println("  supernode start")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	// Add flags
	initCmd.Flags().BoolVar(&initRecover, "recover", false, "Recover key from mnemonic")
	initCmd.Flags().StringVar(&initMnemonic, "mnemonic", "", "Mnemonic for key recovery (required if --recover is specified)")
	initCmd.Flags().StringVar(&initKeyringBackend, "keyring-backend", "", "Keyring backend (test, file, os)")
	initCmd.Flags().StringVar(&initKeyringDir, "keyring-dir", "", "Directory to store keyring files")
	initCmd.Flags().StringVar(&initChainID, "chain-id", "", "Chain ID (required)")

	// Mark required flags
	initCmd.MarkFlagRequired("chain-id")
}
