package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/AlecAivazis/survey/v2"
	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/supernode/config"
	consmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/spf13/cobra"
)

var (
	forceInit          bool
	skipInteractive    bool
	keyringBackendFlag string
)

// Default configuration values
const (
	DefaultKeyringBackend = "test"
	DefaultKeyName        = ""
	DefaultSupernodeAddr  = "0.0.0.0"
	DefaultSupernodePort  = 4444
	DefaultLumeraGRPC     = "localhost:9090"
	DefaultChainID        = "lumera"
)

// InitInputs holds all user inputs for initialization
type InitInputs struct {
	KeyringBackend string
	KeyName        string
	ShouldRecover  bool
	Mnemonic       string
	SupernodeAddr  string
	SupernodePort  int
	LumeraGRPC     string
	ChainID        string
}

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new supernode",
	Long: `Initialize a new supernode by creating a configuration file and setting up keys.

This command will guide you through an interactive setup process to:
1. Create a config.yml file at ~/.supernode
2. Select keyring backend (test, file, or os)
3. Recover an existing key from mnemonic
4. Configure network settings (GRPC address, port, chain ID)

Example:
  supernode init
  supernode init --force  # Override existing installation
  supernode init -y       # Use default values, skip interactive prompts`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Setup base directory
		if err := setupBaseDirectory(); err != nil {
			return err
		}

		// Get user inputs through interactive prompts or use defaults
		inputs, err := gatherUserInputs()
		if err != nil {
			return err
		}

		// Create and setup configuration
		if err := createAndSetupConfig(inputs.KeyName, inputs.ChainID, inputs.KeyringBackend); err != nil {
			return err
		}

		// Setup keyring and handle key creation/recovery (skip if using -y flag)
		var address string
		if !skipInteractive {
			address, err = setupKeyring(inputs.KeyName, inputs.ShouldRecover, inputs.Mnemonic)
			if err != nil {
				return err
			}
		}

		// Update config with gathered settings and save
		if err := updateAndSaveConfig(address, inputs.SupernodeAddr, inputs.SupernodePort, inputs.LumeraGRPC, inputs.ChainID); err != nil {
			return err
		}

		// Print success message
		printSuccessMessage()
		return nil
	},
}

// setupBaseDirectory handles base directory creation and validation
func setupBaseDirectory() error {
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, DefaultBaseDir)
	}

	// Check if base directory already exists
	if _, err := os.Stat(baseDir); err == nil && !forceInit {
		return fmt.Errorf("supernode directory already exists at %s\nUse --force to overwrite or remove the directory manually", baseDir)
	}

	// If force flag is used, clean up config file and keys directory
	if forceInit {
		cfgFile := filepath.Join(baseDir, DefaultConfigFile)
		if err := os.Remove(cfgFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove existing config file: %w", err)
		}

		keysDir := filepath.Join(baseDir, "keys")
		if err := os.RemoveAll(keysDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove existing keys directory: %w", err)
		}

		fmt.Println("Cleaned up existing config file and keys directory")
	}

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return fmt.Errorf("failed to create base directory: %w", err)
	}

	fmt.Printf("BaseDirectory: %s\n", baseDir)
	return nil
}

// gatherUserInputs collects all user inputs through interactive prompts or uses defaults
func gatherUserInputs() (InitInputs, error) {
	if skipInteractive {
		// Use default values when -y flag is set
		fmt.Println("Using default configuration values...")
		backend := DefaultKeyringBackend
		if keyringBackendFlag != "" {
			backend = keyringBackendFlag
		}
		return InitInputs{
			KeyringBackend: backend,
			KeyName:        "",
			ShouldRecover:  false,
			Mnemonic:       "",
			SupernodeAddr:  DefaultSupernodeAddr,
			SupernodePort:  DefaultSupernodePort,
			LumeraGRPC:     DefaultLumeraGRPC,
			ChainID:        DefaultChainID,
		}, nil
	}

	var inputs InitInputs
	var err error

	// Interactive setup
	inputs.KeyringBackend, err = promptKeyringBackend()
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to select keyring backend: %w", err)
	}

	inputs.KeyName, inputs.ShouldRecover, inputs.Mnemonic, err = promptKeyManagement()
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to configure key management: %w", err)
	}

	inputs.SupernodeAddr, inputs.SupernodePort, inputs.LumeraGRPC, inputs.ChainID, err = promptNetworkConfig()
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to configure network settings: %w", err)
	}

	return inputs, nil
}

// createAndSetupConfig creates default configuration and necessary directories
func createAndSetupConfig(keyName, chainID, keyringBackend string) error {
	// Set config file path
	cfgFile := filepath.Join(baseDir, DefaultConfigFile)

	fmt.Printf("Using config file: %s\n", cfgFile)

	// Create default configuration
	appConfig = config.CreateDefaultConfig(keyName, "", chainID, keyringBackend, "")
	appConfig.BaseDir = baseDir

	// Create directories
	if err := appConfig.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	return nil
}

// setupKeyring initializes keyring and handles key creation or recovery
func setupKeyring(keyName string, shouldRecover bool, mnemonic string) (string, error) {
	kr, err := initKeyringFromConfig(appConfig)
	if err != nil {
		return "", fmt.Errorf("failed to initialize keyring: %w", err)
	}

	var address string

	if shouldRecover {
		address, err = recoverExistingKey(kr, keyName, mnemonic)
		if err != nil {
			return "", err
		}
	} else {
		address, err = createNewKey(kr, keyName)
		if err != nil {
			return "", err
		}
	}

	return address, nil
}

// recoverExistingKey handles the recovery of an existing key from mnemonic
func recoverExistingKey(kr consmoskeyring.Keyring, keyName, mnemonic string) (string, error) {
	// Process and validate mnemonic using helper function
	processedMnemonic, err := processAndValidateMnemonic(mnemonic)
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
		// Continue with original mnemonic if validation fails
		processedMnemonic = mnemonic
	}

	info, err := keyring.RecoverAccountFromMnemonic(kr, keyName, processedMnemonic)
	if err != nil {
		return "", fmt.Errorf("failed to recover account: %w", err)
	}

	addr, err := getAddressFromKeyName(kr, keyName)
	if err != nil {
		return "", fmt.Errorf("failed to get address: %w", err)
	}
	address := addr.String()

	fmt.Printf("Key recovered successfully! Name: %s, Address: %s\n", info.Name, address)
	return address, nil
}

// createNewKey handles the creation of a new key
func createNewKey(kr consmoskeyring.Keyring, keyName string) (string, error) {
	// Generate mnemonic and create new account
	keyMnemonic, info, err := keyring.CreateNewAccount(kr, keyName)
	if err != nil {
		return "", fmt.Errorf("failed to create new account: %w", err)
	}

	addr, err := getAddressFromKeyName(kr, keyName)
	if err != nil {
		return "", fmt.Errorf("failed to get address: %w", err)
	}
	address := addr.String()

	fmt.Printf("Key generated successfully! Name: %s, Address: %s, Mnemonic: %s\n", info.Name, address, keyMnemonic)
	fmt.Println("\nIMPORTANT: Write down the mnemonic and keep it in a safe place.")
	fmt.Println("The mnemonic is the only way to recover your account if you forget your password.")

	return address, nil
}

// updateAndSaveConfig updates the configuration with network settings and saves it
func updateAndSaveConfig(address, supernodeAddr string, supernodePort int, lumeraGrpcAddr string, chainID string) error {
	// Update config with address and network settings
	appConfig.SupernodeConfig.Identity = address
	appConfig.SupernodeConfig.IpAddress = supernodeAddr
	appConfig.SupernodeConfig.Port = uint16(supernodePort)
	appConfig.LumeraClientConfig.GRPCAddr = lumeraGrpcAddr
	appConfig.LumeraClientConfig.ChainID = chainID

	// Save config
	cfgFile := filepath.Join(baseDir, DefaultConfigFile)
	if err := config.SaveConfig(appConfig, cfgFile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("\nConfiguration saved to %s\n", cfgFile)
	return nil
}

// printSuccessMessage displays the final success message
func printSuccessMessage() {
	fmt.Println("\nYour supernode has been initialized successfully!")
	fmt.Println("You can now start your supernode with:")
	fmt.Println("  supernode start")
}

// Interactive prompt functions
func promptKeyringBackend() (string, error) {
	var backend string
	prompt := &survey.Select{
		Message: "Choose keyring backend:",
		Options: []string{"os", "file", "test"},
		Default: "os",
		Help:    "os: OS keyring (most secure), file: encrypted file, test: unencrypted (dev only)",
	}
	return backend, survey.AskOne(prompt, &backend)
}

func promptKeyManagement() (keyName string, shouldRecover bool, mnemonic string, err error) {
	shouldRecover = true

	// Key name input with validation
	keyNamePrompt := &survey.Input{
		Message: "Enter key name:",
		Help:    "Alphanumeric characters and underscores only",
	}
	err = survey.AskOne(keyNamePrompt, &keyName, survey.WithValidator(survey.Required))
	if err != nil {
		return "", false, "", err
	}

	// Mnemonic input for recovery
	mnemonicPrompt := &survey.Password{
		Message: "Enter your mnemonic phrase:",
		Help:    "Space-separated words (typically 12 or 24 words)",
	}
	err = survey.AskOne(mnemonicPrompt, &mnemonic, survey.WithValidator(survey.Required))
	if err != nil {
		return "", false, "", err
	}

	return keyName, shouldRecover, mnemonic, nil
}

func promptNetworkConfig() (supernodeAddr string, supernodePort int, lumeraGrpcAddr string, chainID string, err error) {
	// Supernode IP address
	supernodePrompt := &survey.Input{
		Message: "Enter supernode IP address:",
		Default: DefaultSupernodeAddr,
	}
	err = survey.AskOne(supernodePrompt, &supernodeAddr)
	if err != nil {
		return "", 0, "", "", err
	}

	// Supernode port
	var portStr string
	supernodePortPrompt := &survey.Input{
		Message: "Enter supernode port:",
		Default: fmt.Sprintf("%d", DefaultSupernodePort),
	}
	err = survey.AskOne(supernodePortPrompt, &portStr)
	if err != nil {
		return "", 0, "", "", err
	}

	supernodePort, err = strconv.Atoi(portStr)
	if err != nil || supernodePort < 1 || supernodePort > 65535 {
		return "", 0, "", "", fmt.Errorf("invalid supernode port: %s", portStr)
	}

	// Lumera GRPC address (full address with port)
	lumeraPrompt := &survey.Input{
		Message: "Enter Lumera GRPC address:",
		Default: DefaultLumeraGRPC,
	}
	err = survey.AskOne(lumeraPrompt, &lumeraGrpcAddr)
	if err != nil {
		return "", 0, "", "", err
	}

	// Chain ID
	chainPrompt := &survey.Input{
		Message: "Enter chain ID:",
		Default: DefaultChainID,
	}
	err = survey.AskOne(chainPrompt, &chainID, survey.WithValidator(survey.Required))
	if err != nil {
		return "", 0, "", "", err
	}

	return supernodeAddr, supernodePort, lumeraGrpcAddr, chainID, nil
}

func init() {
	rootCmd.AddCommand(initCmd)

	// Add flags
	initCmd.Flags().BoolVar(&forceInit, "force", false, "Force initialization, overwriting existing directory")
	initCmd.Flags().BoolVarP(&skipInteractive, "yes", "y", false, "Skip interactive prompts and use default values")
	initCmd.Flags().StringVar(&keyringBackendFlag, "keyring-backend", "", "Keyring backend to use with -y flag (test, file, os)")
}
