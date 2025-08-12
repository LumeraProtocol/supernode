package cmd

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

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
	keyNameFlag        string
	shouldRecoverFlag  bool
	mnemonicFlag       string
	supernodeAddrFlag  string
	supernodePortFlag  int
	gatewayPortFlag    int
	lumeraGrpcFlag     string
	chainIDFlag        string
	passphrasePlain    string
	passphraseEnv      string
	passphraseFile     string
)

// Default configuration values
const (
	DefaultKeyringBackend = "test"
	DefaultKeyName        = "test-key"
	DefaultSupernodeAddr  = "0.0.0.0"
	DefaultSupernodePort  = 4444
	DefaultGatewayPort    = 8002
	DefaultLumeraGRPC     = "localhost:9090"
	DefaultChainID        = "testing"
)

// InitInputs holds all user inputs for initialization
type InitInputs struct {
	KeyringBackend  string
	PassphrasePlain string
	PassphraseEnv   string
	PassphraseFile  string
	KeyName         string
	ShouldRecover   bool
	Mnemonic        string
	SupernodeAddr   string
	SupernodePort   int
	GatewayPort     int
	LumeraGRPC      string
	ChainID         string
}

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new supernode",
	Long: `Initialize a new supernode by creating a configuration file and setting up keys.


Example:
  supernode init
  supernode init --force  # Override existing installation
  supernode init -y       # Use default values, skip interactive prompts`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Set up signal handling for graceful exit
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		// Create a channel to communicate when the command is done
		done := make(chan struct{})

		// Handle signals in a separate goroutine
		go func() {
			select {
			case <-sigCh:
				fmt.Println("\nInterrupted. Exiting...")
				os.Exit(0)
			case <-done:
				return
			}
		}()

		// Setup base directory
		if err := setupBaseDirectory(); err != nil {
			close(done)
			return err
		}

		// Get user inputs through interactive prompts or use defaults
		inputs, err := gatherUserInputs()
		if err != nil {
			close(done)
			return err
		}

		// Create and setup configuration
		if err := createAndSetupConfig(inputs.KeyName, inputs.ChainID, inputs.KeyringBackend,
			inputs.PassphrasePlain, inputs.PassphraseEnv, inputs.PassphraseFile); err != nil {
			return err
		}

		// Setup keyring and handle key creation/recovery
		var address string
		var generatedMnemonic string

		// Always setup keyring, even in non-interactive mode
		address, generatedMnemonic, err = setupKeyring(inputs.KeyName, inputs.ShouldRecover, inputs.Mnemonic)
		if err != nil {
			return err
		}

		// Update config with gathered settings and save
		if err := updateAndSaveConfig(address, inputs.SupernodeAddr, inputs.SupernodePort, inputs.GatewayPort, inputs.LumeraGRPC, inputs.ChainID); err != nil {
			return err
		}

		// Print success message
		printSuccessMessage(generatedMnemonic)
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

func passphraseFlagCount() int {
	c := 0
	if passphrasePlain != "" {
		c++
	}
	if passphraseEnv != "" {
		c++
	}
	if passphraseFile != "" {
		c++
	}
	return c
}

// gatherUserInputs collects all user inputs through interactive prompts or uses defaults/flags
func gatherUserInputs() (InitInputs, error) {
	// Step 1: Validate that only one passphrase method is specified (plain text, env var, or file)
	if count := passphraseFlagCount(); count > 1 {
		return InitInputs{}, fmt.Errorf("specify only one of --keyring-passphrase, --keyring-passphrase-env, or --keyring-passphrase-file")
	}

	// Step 2: Check if all required parameters are provided via command-line flags
	// This allows for fully automated/scripted initialization without prompts
	allFlagsProvided := keyNameFlag != "" &&
		keyringBackendFlag != "" &&
		supernodeAddrFlag != "" &&
		supernodePortFlag != 0 &&
		gatewayPortFlag != 0 &&
		lumeraGrpcFlag != "" &&
		chainIDFlag != "" &&
		((!shouldRecoverFlag && mnemonicFlag == "") || (shouldRecoverFlag && mnemonicFlag != ""))

	// Step 3: Validate mnemonic requirements for non-interactive mode
	// When recovering a key in non-interactive mode, mnemonic must be provided
	if skipInteractive && shouldRecoverFlag && mnemonicFlag == "" {
		return InitInputs{}, fmt.Errorf("--mnemonic flag is required when --recover flag is set for non-interactive mode")
	}

	// Step 4: Ensure mnemonic is only provided when recovering
	// Prevents accidental exposure of mnemonic when creating new keys
	if !shouldRecoverFlag && mnemonicFlag != "" {
		return InitInputs{}, fmt.Errorf("--mnemonic flag should not be set when not recovering a key")
	}

	// Step 5: Handle non-interactive mode (-y flag) or when all flags are provided
	// Uses command-line flags or defaults without prompting the user
	if skipInteractive || allFlagsProvided {
		fmt.Println("Using provided flags or default configuration values...")

		// Step 5a: Determine keyring backend (how keys are stored securely)
		// Options: 'test' (unencrypted), 'file' (encrypted file), 'os' (system keyring)
		backend := DefaultKeyringBackend
		if keyringBackendFlag != "" {
			backend = keyringBackendFlag
		}

		// Validate keyring backend is one of the allowed options
		if err := validateKeyringBackend(backend); err != nil {
			return InitInputs{}, err
		}

		// Step 5b: Set the name for the cryptographic key
		// This name is used to reference the key in the keyring
		keyName := DefaultKeyName
		if keyNameFlag != "" {
			keyName = keyNameFlag

			// Validate key name only if provided (alphanumeric and underscores only)
			if err := validateKeyName(keyName); err != nil {
				return InitInputs{}, err
			}
		}

		// Step 5c: Configure the supernode's network binding address
		// Determines which network interface the supernode will listen on
		supernodeAddr := DefaultSupernodeAddr
		if supernodeAddrFlag != "" {
			supernodeAddr = supernodeAddrFlag

			// Validate supernode IP address format
			if err := validateIPAddress(supernodeAddr); err != nil {
				return InitInputs{}, err
			}
		}

		// Step 5d: Set the port for supernode peer-to-peer communication
		supernodePort := DefaultSupernodePort
		if supernodePortFlag != 0 {
			supernodePort = supernodePortFlag

			// Ensure port is within valid range (1-65535)
			if supernodePort < 1 || supernodePort > 65535 {
				return InitInputs{}, fmt.Errorf("invalid supernode port: %d, must be between 1 and 65535", supernodePort)
			}
		}

		// Step 5e: Set the HTTP gateway port for API access
		gatewayPort := DefaultGatewayPort
		if gatewayPortFlag != 0 {
			gatewayPort = gatewayPortFlag

			// Validate gateway port is within valid range
			if gatewayPort < 1 || gatewayPort > 65535 {
				return InitInputs{}, fmt.Errorf("invalid gateway port: %d, must be between 1 and 65535", gatewayPort)
			}
		}

		// Step 5f: Configure connection to the Lumera blockchain node
		// This is the GRPC endpoint for blockchain interactions
		lumeraGRPC := DefaultLumeraGRPC
		if lumeraGrpcFlag != "" {
			lumeraGRPC = lumeraGrpcFlag

			// Validate GRPC address format (host:port or schema://host:port)
			if err := validateGRPCAddress(lumeraGRPC); err != nil {
				return InitInputs{}, err
			}
		}

		// Step 5g: Set the blockchain network identifier
		// Must match the chain ID of the Lumera network you're connecting to
		chainID := DefaultChainID
		if chainIDFlag != "" {
			chainID = chainIDFlag
		}

		// Step 5h: Final validation for key recovery mode
		// Ensure mnemonic is provided when attempting to recover an existing key
		if shouldRecoverFlag && mnemonicFlag == "" {
			return InitInputs{}, fmt.Errorf("--mnemonic flag is required when --recover flag is set")
		}

		// Step 5i: Return all collected configuration values
		// These will be used to initialize the supernode
		return InitInputs{
			KeyringBackend:  backend,
			PassphrasePlain: passphrasePlain,
			PassphraseEnv:   passphraseEnv,
			PassphraseFile:  passphraseFile,
			KeyName:         keyName,
			ShouldRecover:   shouldRecoverFlag,
			Mnemonic:        mnemonicFlag,
			SupernodeAddr:   supernodeAddr,
			SupernodePort:   supernodePort,
			GatewayPort:     gatewayPort,
			LumeraGRPC:      lumeraGRPC,
			ChainID:         chainID,
		}, nil
	}

	// Step 6: Interactive mode - prompt user for each configuration value
	// This path is taken when -y flag is NOT provided AND not all required flags are specified
	// Even if some flags are provided, missing values will be prompted interactively
	var inputs InitInputs
	var err error

	// Step 6a: Prompt for keyring backend selection
	// User chooses how to securely store their cryptographic keys
	inputs.KeyringBackend, err = promptKeyringBackend(keyringBackendFlag)
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to select keyring backend: %w", err)
	}

	// Step 6b: Handle passphrase requirements for secure keyring backends
	// 'file' and 'os' backends require a passphrase for encryption
	backend := strings.ToLower(inputs.KeyringBackend)
	switch backend {
	case "file", "os":
		// These backends require encryption passphrase
		switch passphraseFlagCount() {
		case 0:
			// No passphrase provided via flags, prompt user interactively
			prompt := &survey.Password{
				Message: "Enter keyring passphrase:",
				Help:    "Required for 'file' or 'os' keyring back-ends – Ctrl-C to abort.",
			}
			if err = survey.AskOne(prompt, &inputs.PassphrasePlain, survey.WithValidator(survey.Required)); err != nil {
				return InitInputs{}, fmt.Errorf("failed to get keyring passphrase: %w", err)
			}
		case 1:
			// Passphrase provided via one of the flag methods
			inputs.PassphrasePlain = passphrasePlain
			inputs.PassphraseEnv = passphraseEnv
			inputs.PassphraseFile = passphraseFile
		default:
			// Multiple passphrase methods specified (error case)
			return InitInputs{}, fmt.Errorf("specify only one of --keyring-passphrase, --keyring-passphrase-env, or --keyring-passphrase-file")
		}

	default:
		// 'test' backend doesn't require passphrase (unencrypted - dev only)
		inputs.PassphrasePlain = passphrasePlain
		inputs.PassphraseEnv = passphraseEnv
		inputs.PassphraseFile = passphraseFile
	}

	// Step 6c: Prompt for key management settings
	// User chooses to create new key or recover existing one
	inputs.KeyName, inputs.ShouldRecover, inputs.Mnemonic, err = promptKeyManagement(keyNameFlag, shouldRecoverFlag, mnemonicFlag)
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to configure key management: %w", err)
	}

	// Step 6d: Prompt for network configuration
	// Collect all network-related settings (ports, addresses, chain ID)
	inputs.SupernodeAddr, inputs.SupernodePort, inputs.GatewayPort, inputs.LumeraGRPC, inputs.ChainID, err =
		promptNetworkConfig(supernodeAddrFlag, supernodePortFlag, gatewayPortFlag, lumeraGrpcFlag, chainIDFlag)
	if err != nil {
		return InitInputs{}, fmt.Errorf("failed to configure network settings: %w", err)
	}

	return inputs, nil
}

// createAndSetupConfig creates default configuration and necessary directories
func createAndSetupConfig(keyName, chainID, keyringBackend, passPlain, passEnv, passFile string) error {
	// Set config file path
	cfgFile := filepath.Join(baseDir, DefaultConfigFile)

	fmt.Printf("Using config file: %s\n", cfgFile)

	// Create default configuration
	appConfig = config.CreateDefaultConfig(keyName, "", chainID, keyringBackend, "", passPlain, passEnv, passFile)
	appConfig.BaseDir = baseDir

	// Create directories
	if err := appConfig.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	return nil
}

// setupKeyring initializes keyring and handles key creation or recovery
// Returns address and mnemonic (if a new key was created)
func setupKeyring(keyName string, shouldRecover bool, mnemonic string) (string, string, error) {
	kr, err := initKeyringFromConfig(appConfig)
	if err != nil {
		return "", "", fmt.Errorf("failed to initialize keyring: %w", err)
	}

	var address string
	var generatedMnemonic string

	if shouldRecover {
		address, err = recoverExistingKey(kr, keyName, mnemonic)
		if err != nil {
			return "", "", err
		}
	} else {
		address, generatedMnemonic, err = createNewKey(kr, keyName)
		if err != nil {
			return "", "", err

		}
	}

	return address, generatedMnemonic, nil
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
func createNewKey(kr consmoskeyring.Keyring, keyName string) (string, string, error) {
	// Generate mnemonic and create new account
	keyMnemonic, _, err := keyring.CreateNewAccount(kr, keyName)
	if err != nil {
		return "", "", fmt.Errorf("failed to create new account: %w", err)
	}

	addr, err := getAddressFromKeyName(kr, keyName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get address: %w", err)
	}
	address := addr.String()

	return address, keyMnemonic, nil
}

// updateAndSaveConfig updates the configuration with network settings and saves it
func updateAndSaveConfig(address, supernodeAddr string, supernodePort int, gatewayPort int, lumeraGrpcAddr string, chainID string) error {
	// Update config with address and network settings
	appConfig.SupernodeConfig.Identity = address
	appConfig.SupernodeConfig.IpAddress = supernodeAddr
	appConfig.SupernodeConfig.Port = uint16(supernodePort)
	appConfig.SupernodeConfig.GatewayPort = uint16(gatewayPort)
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
func printSuccessMessage(mnemonic string) {
	fmt.Println("\nYour supernode has been initialized successfully!")

	// If a mnemonic was generated, display it again
	if mnemonic != "" {
		fmt.Println("\nIMPORTANT: Make sure you have saved your mnemonic:")
		fmt.Printf("Mnemonic: %s\n", mnemonic)
	}

	fmt.Println("\nYou can now start your supernode with:")
	fmt.Println("  supernode start")
}

// Interactive prompt functions
func promptKeyringBackend(passedBackend string) (string, error) {
	var backend string
	if passedBackend != "" {
		if passedBackend != "os" && passedBackend != "file" && passedBackend != "test" {
			return "", fmt.Errorf("invalid keyring backend: %s, must be one of 'os', 'file', or 'test'", passedBackend)
		}
		backend = passedBackend
	} else {
		backend = DefaultKeyringBackend
	}
	prompt := &survey.Select{
		Message: "Choose keyring backend:",
		Options: []string{"os", "file", "test"},
		Default: backend,
		Help:    "os: OS keyring (most secure), file: encrypted file, test: unencrypted (dev only), Ctrl-C for exit",
	}
	return backend, survey.AskOne(prompt, &backend)
}

func promptKeyManagement(passedKeyName string, recover bool, passedMnemonic string) (keyName string, shouldRecover bool, mnemonic string, err error) {
	// Key name input with validation
	keyNamePrompt := &survey.Input{
		Message: "Enter key name:",
		Help:    "Alphanumeric characters and underscores only, Ctrl-C for exit",
		Default: passedKeyName,
	}
	err = survey.AskOne(keyNamePrompt, &keyName, survey.WithValidator(survey.Required))
	if err != nil {
		return "", false, "", err
	}

	// Validate key name format if provided
	if keyName != "" {
		if err := validateKeyName(keyName); err != nil {
			return "", false, "", err
		}
	}

	shouldRecover = recover
	if !recover {
		// Ask whether to create a new address or recover from mnemonic
		createOrRecoverPrompt := &survey.Select{
			Message: "Would you like to create a new address or recover from mnemonic?",
			Options: []string{"Create new address", "Recover from mnemonic"},
			Default: "Create new address",
			Help:    "Create a new address or recover an existing one from mnemonic, Ctrl-C for exit",
		}
		var createOrRecover string
		err = survey.AskOne(createOrRecoverPrompt, &createOrRecover)
		if err != nil {
			return "", false, "", err
		}

		shouldRecover = createOrRecover == "Recover from mnemonic"
	}

	// If recovering, ask for mnemonic
	mnemonic = passedMnemonic
	if shouldRecover && passedMnemonic == "" {
		mnemonicPrompt := &survey.Password{
			Message: "Enter your mnemonic phrase:",
			Help:    "Space-separated words (typically 12 or 24 words), Ctrl-C for exit",
		}
		err = survey.AskOne(mnemonicPrompt, &mnemonic, survey.WithValidator(survey.Required))
		if err != nil {
			return "", false, "", err
		}
	}

	return keyName, shouldRecover, mnemonic, nil
}

func promptNetworkConfig(passedAddrs string, passedPort int, passedGatewayPort int, passedGRPC, passedChainID string) (supernodeAddr string, supernodePort int, gatewayPort int, lumeraGrpcAddr string, chainID string, err error) {
	if passedAddrs != "" {
		supernodeAddr = passedAddrs
	} else {
		supernodeAddr = DefaultSupernodeAddr
	}
	var port string
	if passedPort != 0 {
		port = fmt.Sprintf("%d", passedPort)
	} else {
		port = fmt.Sprintf("%d", DefaultSupernodePort)
	}

	var gPort string
	if passedGatewayPort != 0 {
		gPort = fmt.Sprintf("%d", passedGatewayPort)
	} else {
		gPort = fmt.Sprintf("%d", DefaultGatewayPort)
	}
	if passedGRPC != "" {
		lumeraGrpcAddr = passedGRPC
	} else {
		lumeraGrpcAddr = DefaultLumeraGRPC
	}
	if passedChainID != "" {
		chainID = passedChainID
	} else {
		chainID = DefaultChainID
	}

	// Supernode IP address
	supernodePrompt := &survey.Input{
		Message: "Enter supernode IP address:",
		Default: supernodeAddr,
		Help:    "IP address for the supernode to listen on, Ctrl-C for exit",
	}
	err = survey.AskOne(supernodePrompt, &supernodeAddr)
	if err != nil {
		return "", 0, 0, "", "", err
	}

	// Validate IP address format
	if err := validateIPAddress(supernodeAddr); err != nil {
		return "", 0, 0, "", "", err
	}

	// Supernode port
	var portStr string
	supernodePortPrompt := &survey.Input{
		Message: "Enter supernode port:",
		Default: port,
		Help:    "Port for the supernode to listen on (1-65535), Ctrl-C for exit",
	}
	err = survey.AskOne(supernodePortPrompt, &portStr)
	if err != nil {
		return "", 0, 0, "", "", err
	}

	supernodePort, err = strconv.Atoi(portStr)
	if err != nil || supernodePort < 1 || supernodePort > 65535 {
		return "", 0, 0, "", "", fmt.Errorf("invalid supernode port: %s", portStr)
	}

	// Gateway port
	var gatewayPortStr string
	gatewayPortPrompt := &survey.Input{
		Message: "Enter HTTP gateway port:",
		Default: gPort,
		Help:    "Port for the HTTP gateway to listen on (1-65535), Ctrl-C for exit",
	}
	err = survey.AskOne(gatewayPortPrompt, &gatewayPortStr)
	if err != nil {
		return "", 0, 0, "", "", err
	}

	gatewayPort, err = strconv.Atoi(gatewayPortStr)
	if err != nil || gatewayPort < 1 || gatewayPort > 65535 {
		return "", 0, 0, "", "", fmt.Errorf("invalid gateway port: %s", gatewayPortStr)
	}

	// Lumera GRPC address (full address with port)
	lumeraPrompt := &survey.Input{
		Message: "Enter Lumera GRPC address:",
		Default: lumeraGrpcAddr,
		Help:    "GRPC address of the Lumera node (host:port), Ctrl-C for exit",
	}
	err = survey.AskOne(lumeraPrompt, &lumeraGrpcAddr)
	if err != nil {
		return "", 0, 0, "", "", err
	}

	// Validate GRPC address format
	if err := validateGRPCAddress(lumeraGrpcAddr); err != nil {
		return "", 0, 0, "", "", err
	}

	// Chain ID
	chainPrompt := &survey.Input{
		Message: "Enter chain ID:",
		Default: chainID,
		Help:    "Chain ID of the Lumera network, Ctrl-C for exit",
	}
	err = survey.AskOne(chainPrompt, &chainID, survey.WithValidator(survey.Required))
	if err != nil {
		return "", 0, 0, "", "", err
	}

	return supernodeAddr, supernodePort, gatewayPort, lumeraGrpcAddr, chainID, nil
}

// validateKeyringBackend checks if the provided keyring backend is valid
func validateKeyringBackend(backend string) error {
	if backend != "os" && backend != "file" && backend != "test" {
		return fmt.Errorf("invalid keyring backend: %s, must be one of 'os', 'file', or 'test'", backend)
	}
	return nil
}

// validateKeyName checks if the provided key name contains only alphanumeric characters and underscores
func validateKeyName(keyName string) error {
	if keyName == "" {
		return fmt.Errorf("key name cannot be empty")
	}

	matched, err := regexp.MatchString("^[a-zA-Z0-9_]+$", keyName)
	if err != nil {
		return fmt.Errorf("error validating key name: %w", err)
	}

	if !matched {
		return fmt.Errorf("invalid key name: %s, must contain only alphanumeric characters and underscores", keyName)
	}

	return nil
}

// validateIPAddress checks if the provided string is a valid IP address
func validateIPAddress(ipAddress string) error {
	if ipAddress == "" {
		return fmt.Errorf("IP address cannot be empty")
	}

	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return fmt.Errorf("invalid IP address format: %s", ipAddress)
	}

	return nil
}

// validateGRPCAddress checks if the provided string follows valid GRPC address formats:
// - host:port
// - schema://hostname-or-ip
// - schema://hostname-or-ip:port
func validateGRPCAddress(grpcAddress string) error {
	if grpcAddress == "" {
		return fmt.Errorf("GRPC address cannot be empty")
	}

	// Check if the address has a schema (starts with schema://)
	if strings.Contains(grpcAddress, "://") {
		// Parse as URL
		parsedURL, err := url.Parse(grpcAddress)
		if err != nil {
			return fmt.Errorf("invalid GRPC address format: %s, error: %v", grpcAddress, err)
		}

		// Validate host part
		if parsedURL.Host == "" {
			return fmt.Errorf("host part of GRPC address cannot be empty")
		}

		// If port is specified, validate it
		if parsedURL.Port() != "" {
			portNum, err := strconv.Atoi(parsedURL.Port())
			if err != nil || portNum < 1 || portNum > 65535 {
				return fmt.Errorf("invalid port in GRPC address: %s, must be a number between 1 and 65535", parsedURL.Port())
			}
		}
	} else {
		// No schema, should be in host:port format
		host, port, err := net.SplitHostPort(grpcAddress)
		if err != nil {
			return fmt.Errorf("invalid GRPC address format: %s, must be in host:port format or schema://host[:port] format", grpcAddress)
		}

		// Validate host part
		if host == "" {
			return fmt.Errorf("host part of GRPC address cannot be empty")
		}

		// Validate port part
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("invalid port in GRPC address: %s, must be a number between 1 and 65535", port)
		}
	}

	return nil
}

func init() {
	rootCmd.AddCommand(initCmd)

	// Add flags
	initCmd.Flags().BoolVar(&forceInit, "force", false, "Force initialization, overwriting existing directory")
	initCmd.Flags().BoolVarP(&skipInteractive, "yes", "y", false, "Skip interactive prompts and use default values")
	initCmd.Flags().StringVar(&keyringBackendFlag, "keyring-backend", "", "Keyring backend to use with -y flag (test, file, os)")
	initCmd.Flags().StringVar(&keyNameFlag, "key-name", "", "Name of the key to create or recover")
	initCmd.Flags().BoolVar(&shouldRecoverFlag, "recover", false, "Recover an existing key from mnemonic")
	initCmd.Flags().StringVar(&mnemonicFlag, "mnemonic", "", "Mnemonic phrase for key recovery (only used with --recover)")
	initCmd.Flags().StringVar(&supernodeAddrFlag, "supernode-addr", "", "IP address for the supernode to listen on")
	initCmd.Flags().IntVar(&supernodePortFlag, "supernode-port", 0, "Port for the supernode to listen on")
	initCmd.Flags().IntVar(&gatewayPortFlag, "gateway-port", 0, "Port for the HTTP gateway to listen on")
	initCmd.Flags().StringVar(&lumeraGrpcFlag, "lumera-grpc", "", "GRPC address of the Lumera node (host:port)")
	initCmd.Flags().StringVar(&chainIDFlag, "chain-id", "", "Chain ID of the Lumera network")
	initCmd.Flags().StringVar(&passphrasePlain, "keyring-passphrase", "", "Keyring passphrase for non-interactive mode")
	initCmd.Flags().StringVar(&passphraseEnv, "keyring-passphrase-env", "", "Environment variable containing keyring passphrase")
	initCmd.Flags().StringVar(&passphraseFile, "keyring-passphrase-file", "", "File containing keyring passphrase")
}
