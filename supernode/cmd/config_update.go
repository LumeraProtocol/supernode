package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/AlecAivazis/survey/v2"
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/spf13/cobra"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// configUpdateCmd represents the config update command
var configUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update configuration parameters",
	Long:  `Interactively update configuration parameters.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Show parameter selection menu
		paramToUpdate, err := promptParameterSelection()
		if err != nil {
			return fmt.Errorf("failed to select parameter: %w", err)
		}

		// Update the selected parameter
		switch paramToUpdate {
		case "Supernode IP Address":
			return updateSupernodeIP()
		case "Supernode Port":
			return updateSupernodePort()
		case "Lumera GRPC Address":
			return updateLumeraGRPC()
		case "Chain ID":
			return updateChainID()
		case "Key Name":
			return updateKeyName()
		case "Keyring Backend":
			return updateKeyringBackend()
		}

		return nil
	},
}

// promptParameterSelection shows menu of updatable parameters
func promptParameterSelection() (string, error) {
	var param string
	prompt := &survey.Select{
		Message: "Select parameter to update:",
		Options: []string{
			"Supernode IP Address",
			"Supernode Port", 
			"Lumera GRPC Address",
			"Chain ID",
			"Key Name",
			"Keyring Backend",
		},
	}
	return param, survey.AskOne(prompt, &param)
}

// Simple field updates
func updateSupernodeIP() error {
	var newIP string
	prompt := &survey.Input{
		Message: "Enter new supernode host address:",
		Default: appConfig.SupernodeConfig.Host,
	}
	if err := survey.AskOne(prompt, &newIP); err != nil {
		return err
	}

	appConfig.SupernodeConfig.Host = newIP
	return saveConfig()
}

func updateSupernodePort() error {
	var portStr string
	prompt := &survey.Input{
		Message: "Enter new supernode port:",
		Default: fmt.Sprintf("%d", appConfig.SupernodeConfig.Port),
	}
	if err := survey.AskOne(prompt, &portStr); err != nil {
		return err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port number: %s", portStr)
	}

	appConfig.SupernodeConfig.Port = uint16(port)
	return saveConfig()
}

func updateLumeraGRPC() error {
	var newAddr string
	prompt := &survey.Input{
		Message: "Enter new Lumera GRPC address:",
		Default: appConfig.LumeraClientConfig.GRPCAddr,
	}
	if err := survey.AskOne(prompt, &newAddr); err != nil {
		return err
	}

	appConfig.LumeraClientConfig.GRPCAddr = newAddr
	return saveConfig()
}

func updateChainID() error {
	var newChainID string
	prompt := &survey.Input{
		Message: "Enter new chain ID:",
		Default: appConfig.LumeraClientConfig.ChainID,
	}
	if err := survey.AskOne(prompt, &newChainID, survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	appConfig.LumeraClientConfig.ChainID = newChainID
	return saveConfig()
}

// Key name update with key selection
func updateKeyName() error {
	// Initialize keyring
	kr, err := initKeyringFromConfig(appConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize keyring: %w", err)
	}

	// Get existing keys
	keyInfos, err := kr.List()
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	return selectKeyFromKeyring(kr, keyInfos)
}

func addNewKey(kr cKeyring.Keyring) error {
	// Prompt for key name
	var keyName string
	namePrompt := &survey.Input{
		Message: "Enter key name:",
		Help:    "Alphanumeric characters and underscores only",
	}
	if err := survey.AskOne(namePrompt, &keyName, survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	// Prompt for mnemonic
	var mnemonic string
	mnemonicPrompt := &survey.Password{
		Message: "Enter mnemonic phrase:",
		Help:    "Space-separated words (typically 12 or 24 words)",
	}
	if err := survey.AskOne(mnemonicPrompt, &mnemonic, survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	// Process and validate mnemonic
	processedMnemonic, err := processAndValidateMnemonic(mnemonic)
	if err != nil {
		return err
	}

	// Recover account
	_, err = keyring.RecoverAccountFromMnemonic(kr, keyName, processedMnemonic)
	if err != nil {
		return fmt.Errorf("failed to recover account: %w", err)
	}

	// Update config
	return selectExistingKey(kr, keyName)
}

func selectExistingKey(kr cKeyring.Keyring, keyName string) error {
	// Get address
	addr, err := getAddressFromKeyName(kr, keyName)
	if err != nil {
		return fmt.Errorf("failed to get address: %w", err)
	}

	// Update config
	appConfig.SupernodeConfig.KeyName = keyName
	appConfig.SupernodeConfig.Identity = addr.String()

	fmt.Printf("Updated key to: %s (%s)\n", keyName, addr.String())
	return saveConfig()
}

// Keyring backend update with warning
func updateKeyringBackend() error {
	// Show warning
	fmt.Println("⚠️  WARNING: Changing keyring backend will switch to a different keyring.")
	fmt.Println("You will need to select a key from the new keyring or recover one.")
	
	var proceed bool
	confirmPrompt := &survey.Confirm{
		Message: "Do you want to continue?",
		Default: false,
	}
	if err := survey.AskOne(confirmPrompt, &proceed); err != nil {
		return err
	}

	if !proceed {
		fmt.Println("Operation cancelled.")
		return nil
	}

	// Select new backend
	var backend string
	prompt := &survey.Select{
		Message: "Choose new keyring backend:",
		Options: []string{"os", "file", "test"},
		Default: appConfig.KeyringConfig.Backend,
	}
	if err := survey.AskOne(prompt, &backend); err != nil {
		return err
	}

	// Update keyring backend in config
	appConfig.KeyringConfig.Backend = backend
	
	// Save config with new keyring backend
	if err := saveConfig(); err != nil {
		return err
	}

	fmt.Printf("Updated keyring backend to: %s\n", backend)
	
	// Reload config to get the new keyring settings
	cfgFile := filepath.Join(baseDir, DefaultConfigFile)
	reloadedConfig, err := config.LoadConfig(cfgFile, baseDir)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}
	appConfig = reloadedConfig

	// Initialize new keyring
	kr, err := initKeyringFromConfig(appConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize new keyring: %w", err)
	}

	// Check for existing keys in new keyring
	keyInfos, err := kr.List()
	if err != nil {
		return fmt.Errorf("failed to list keys in new keyring: %w", err)
	}

	// Show available keys and let user select
	return selectKeyFromNewKeyring(kr, keyInfos)
}

func selectKeyFromNewKeyring(kr cKeyring.Keyring, keyInfos []*cKeyring.Record) error {
	if len(keyInfos) == 0 {
		fmt.Println("No existing keys found in the new keyring.")
	} else {
		fmt.Printf("Found %d key(s) in the new keyring.\n", len(keyInfos))
	}
	return selectKeyFromKeyring(kr, keyInfos)
}

func selectKeyFromKeyring(kr cKeyring.Keyring, keyInfos []*cKeyring.Record) error {
	// Build options list with display format
	options := []string{}
	
	// Add existing keys
	for _, info := range keyInfos {
		addr, err := info.GetAddress()
		if err != nil {
			continue
		}
		options = append(options, fmt.Sprintf("%s (%s)", info.Name, addr.String()))
	}
	
	// Always add option to recover new key
	options = append(options, "Add new key (recover from mnemonic)")

	// Show selection
	var selectedIndex int
	prompt := &survey.Select{
		Message: "Select key:",
		Options: options,
	}
	if err := survey.AskOne(prompt, &selectedIndex); err != nil {
		return err
	}

	// Handle selection using index instead of parsing strings
	if selectedIndex == len(keyInfos) {
		// Last option is "Add new key"
		return addNewKey(kr)
	} else {
		// Use the selected index to get the actual key info
		selectedKey := keyInfos[selectedIndex]
		return selectExistingKey(kr, selectedKey.Name)
	}
}

// saveConfig saves the updated configuration
func saveConfig() error {
	cfgFile := filepath.Join(baseDir, DefaultConfigFile)
	if err := config.SaveConfig(appConfig, cfgFile); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Configuration updated successfully: %s\n", cfgFile)
	return nil
}

func init() {
	configCmd.AddCommand(configUpdateCmd)
}