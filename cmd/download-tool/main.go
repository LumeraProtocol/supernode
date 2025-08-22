package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	sdklog "github.com/LumeraProtocol/supernode/v2/sdk/log"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

func main() {
	var (
		actionID       = flag.String("action", "", "Action ID to download (required)")
		outputPath     = flag.String("out", "", "Output file path (optional, defaults to action_id.dat)")
		signature      = flag.String("signature", "", "Signature for verification (optional)")
		timeout        = flag.Duration("timeout", 5*time.Minute, "Download timeout")
		keyringBackend = flag.String("keyring-backend", "", "Keyring backend (os, file, test)")
		keyringDir     = flag.String("keyring-dir", "", "Keyring directory (optional)")
		keyName        = flag.String("key-name", "", "Key name in keyring")
		localAddr      = flag.String("local-addr", "", "Local cosmos address")
		lumeraGRPC     = flag.String("lumera-grpc", "", "Lumera gRPC endpoint")
		chainID        = flag.String("chain-id", "", "Chain ID")
		help           = flag.Bool("help", false, "Show help")
		autoDetect     = flag.Bool("auto-detect", true, "Auto-detect config from ~/.supernode (default true)")
		listKeys       = flag.Bool("list-keys", false, "List available keys in keyring and exit")
	)
	flag.Parse()

	if *help {
		showHelp()
		return
	}

	// Auto-detect supernode configuration if enabled
	var detected AutoDetectedConfig
	if *autoDetect {
		detected = detectSupernodeConfig()
		if detected.FoundConfig {
			fmt.Printf("📋 Auto-detected supernode config: %s\n", detected.ConfigPath)
		}
	}

	// Handle list-keys command
	if *listKeys {
		showAvailableKeys(detected)
		return
	}

	// Apply auto-detected values as defaults (can be overridden by flags)
	if *keyringBackend == "" && detected.KeyringBackend != "" {
		*keyringBackend = detected.KeyringBackend
	}
	if *keyringDir == "" && detected.KeyringDir != "" {
		*keyringDir = detected.KeyringDir
	}
	if *keyName == "" && detected.KeyName != "" {
		*keyName = detected.KeyName
	}
	if *localAddr == "" && detected.LocalAddr != "" {
		*localAddr = detected.LocalAddr
	}
	if *lumeraGRPC == "" && detected.LumeraGRPC != "" {
		*lumeraGRPC = detected.LumeraGRPC
	}
	if *chainID == "" && detected.ChainID != "" {
		*chainID = detected.ChainID
	}

	// Set final defaults
	if *keyringBackend == "" {
		*keyringBackend = "os"
	}
	if *lumeraGRPC == "" {
		*lumeraGRPC = "localhost:9090"
	}
	if *chainID == "" {
		*chainID = "lumera"
	}

	// Validate required parameters
	if *actionID == "" {
		log.Fatal("❌ Required parameter missing: -action\n\nUse -help for usage information")
	}

	if *keyName == "" {
		if detected.FoundConfig {
			log.Fatal("❌ Key name not found in config and not provided via -key-name\n\nUse -list-keys to see available keys")
		} else {
			log.Fatal("❌ Required parameter missing: -key-name\n\nUse -help for usage information or -list-keys to see available keys")
		}
	}
	if *localAddr == "" {
		if detected.FoundConfig {
			log.Fatal("❌ Could not auto-detect local address from keyring\n\nPlease provide -local-addr manually or check your keyring configuration")
		} else {
			log.Fatal("❌ Required parameter missing: -local-addr\n\nUse -help for usage information")
		}
	}

	// Set default output path if not provided
	if *outputPath == "" {
		*outputPath = *actionID + ".dat"
	}

	// Show configuration info
	fmt.Printf("🔐 Using keyring: %s", *keyringBackend)
	if *keyringDir != "" {
		fmt.Printf(" (dir: %s)", *keyringDir)
	}
	fmt.Println()
	if detected.FoundConfig {
		fmt.Printf("🔑 Using key: %s (auto-detected)\n", *keyName)
		// Show whether address came from identity field or keyring derivation
		if detected.LocalAddr == *localAddr {
			fmt.Printf("👤 Local address: %s (from config identity)\n", *localAddr)
		} else {
			fmt.Printf("👤 Local address: %s (auto-detected)\n", *localAddr)
		}
	} else {
		fmt.Printf("🔑 Using key: %s\n", *keyName)
		fmt.Printf("👤 Local address: %s\n", *localAddr)
	}
	fmt.Printf("📥 Downloading action ID: %s\n", *actionID)
	fmt.Printf("💾 Output file: %s\n", *outputPath)

	if err := downloadFile(*actionID, *signature, *outputPath, *timeout,
		*keyringBackend, *keyringDir, *keyName, *localAddr, *lumeraGRPC, *chainID); err != nil {
		log.Fatalf("❌ Download failed: %v", err)
	}

	fmt.Printf("✅ Download completed successfully: %s\n", *outputPath)
}

func showHelp() {
	fmt.Println(`Supernode File Download Tool

A tool to download files from supernode using cascade action IDs. Uses SDK auto-discovery
to find and try available supernodes automatically. Automatically detects configuration
from ~/.supernode/ if available, or accepts manual configuration.

Usage:
  ./download-tool -action <action_id> [options]

Required:
  -action <id>          Action ID to download

Auto-Detection (from ~/.supernode/config.yaml):
  The tool automatically detects keyring settings, key names, and addresses from
  your supernode configuration. Manual flags override auto-detected values.

Manual Configuration (when auto-detection unavailable):
  -key-name <key>       Key name in keyring  
  -local-addr <addr>    Your local cosmos address

Options:
  -out <path>          Output file path (default: <action_id>.dat)
  -signature <sig>     Signature for verification (optional)
  -timeout <duration>  Download timeout (default: 5m)
  -keyring-backend <b> Keyring backend: os, file, test (auto-detected or default: os)
  -keyring-dir <dir>   Keyring directory (auto-detected or default)
  -lumera-grpc <addr>  Lumera gRPC endpoint (auto-detected or default: localhost:9090)
  -chain-id <id>       Chain ID (auto-detected or default: lumera)
  -auto-detect <bool>  Enable auto-detection (default: true)
  -list-keys           List available keys in keyring and exit
  -help                Show this help

Examples:
  # With auto-detection (recommended)
  ./download-tool -action abc123
  
  # With manual configuration  
  ./download-tool -action abc123 -key-name mykey -local-addr lumera1abc...
  
  # List available keys
  ./download-tool -list-keys
  
  # Override auto-detected chain
  ./download-tool -action abc123 -chain-id mainnet`)
}

func downloadFile(actionID, signature, outputPath string, timeout time.Duration,
	keyringBackend, keyringDir, keyName, localAddr, lumeraGRPC, chainID string) error {

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	keyring.InitSDKConfig()

	kr, err := keyring.InitKeyring(config.KeyringConfig{
		Backend: keyringBackend,
		Dir:     keyringDir,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize keyring: %w", err)
	}

	// Create action client configuration (exactly like e2e test)
	actionConfig := sdkconfig.Config{
		Account: sdkconfig.AccountConfig{
			LocalCosmosAddress: localAddr,
			KeyName:            keyName,
			Keyring:            kr,
			PeerType:           securekeyx.Simplenode, // Client peer type
		},
		Lumera: sdkconfig.LumeraConfig{
			GRPCAddr: lumeraGRPC,
			ChainID:  chainID,
		},
	}

	actionClient, err := action.NewClient(ctx, actionConfig, sdklog.NewNoopLogger())
	if err != nil {
		return fmt.Errorf("failed to create action client: %w", err)
	}

	fmt.Printf("🚀 Starting secure download using SDK action client...\n")

	// Download using the same method as e2e test
	taskID, err := actionClient.DownloadCascade(ctx, actionID, filepath.Dir(outputPath), signature)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Printf("📋 Download task created: %s\n", taskID)

	// Wait a moment for download to complete (like e2e test)
	time.Sleep(3 * time.Second)

	// Check if file was created
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return fmt.Errorf("output file was not created: %s", outputPath)
	}

	// Get file info for reporting
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	fmt.Printf("📊 Download completed: %s (%d bytes)\n", formatBytes(fileInfo.Size()), fileInfo.Size())

	return nil
}

// AutoDetectedConfig holds auto-detected configuration values
type AutoDetectedConfig struct {
	KeyName        string
	LocalAddr      string
	KeyringBackend string
	KeyringDir     string
	LumeraGRPC     string
	ChainID        string
	ConfigPath     string
	FoundConfig    bool
}

// detectSupernodeConfig attempts to auto-detect supernode configuration
func detectSupernodeConfig() AutoDetectedConfig {
	result := AutoDetectedConfig{}

	// Try to find supernode config in default location
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return result
	}

	configPath := filepath.Join(homeDir, ".supernode", "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return result
	}

	// Load the supernode config
	baseDir := filepath.Join(homeDir, ".supernode")
	supernodeConfig, err := config.LoadConfig(configPath, baseDir)
	if err != nil {
		return result
	}

	result.ConfigPath = configPath
	result.FoundConfig = true
	result.KeyName = supernodeConfig.SupernodeConfig.KeyName
	result.KeyringBackend = supernodeConfig.KeyringConfig.Backend
	result.KeyringDir = supernodeConfig.GetKeyringDir()
	result.LumeraGRPC = supernodeConfig.LumeraClientConfig.GRPCAddr
	result.ChainID = supernodeConfig.LumeraClientConfig.ChainID

	// Use identity from config if available (more reliable than keyring derivation)
	if supernodeConfig.SupernodeConfig.Identity != "" {
		result.LocalAddr = supernodeConfig.SupernodeConfig.Identity
	} else if result.KeyName != "" {
		// Fallback: derive from keyring if identity not set in config
		localAddr, err := getLocalAddressFromKeyring(supernodeConfig.KeyringConfig, result.KeyName)
		if err == nil {
			result.LocalAddr = localAddr
		}
	}

	return result
}

// getLocalAddressFromKeyring attempts to get the local cosmos address from keyring
func getLocalAddressFromKeyring(keyringConfig config.KeyringConfig, keyName string) (string, error) {
	// Initialize SDK config
	keyring.InitSDKConfig()

	// Initialize keyring
	kr, err := keyring.InitKeyring(keyringConfig)
	if err != nil {
		return "", fmt.Errorf("failed to initialize keyring: %w", err)
	}

	// Get the key from keyring
	keyInfo, err := kr.Key(keyName)
	if err != nil {
		return "", fmt.Errorf("key not found: %w", err)
	}

	// Get address from key
	address, err := keyInfo.GetAddress()
	if err != nil {
		return "", fmt.Errorf("failed to get address from key: %w", err)
	}

	return address.String(), nil
}

// listKeysInKeyring lists all keys in the keyring for user selection
func listKeysInKeyring(keyringConfig config.KeyringConfig) ([]string, error) {
	// Initialize SDK config
	keyring.InitSDKConfig()

	// Initialize keyring
	kr, err := keyring.InitKeyring(keyringConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize keyring: %w", err)
	}

	// List all keys
	keyInfos, err := kr.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}

	var keyNames []string
	for _, keyInfo := range keyInfos {
		keyNames = append(keyNames, keyInfo.Name)
	}

	return keyNames, nil
}

// showAvailableKeys displays available keys in the keyring
func showAvailableKeys(detected AutoDetectedConfig) {
	fmt.Println("Available keys in keyring:")
	fmt.Println("=========================")

	if !detected.FoundConfig {
		fmt.Println("❌ No supernode config found at ~/.supernode/config.yaml")
		fmt.Println("Please ensure supernode is initialized or provide keyring parameters manually.")
		return
	}

	keys, err := listKeysInKeyring(config.KeyringConfig{
		Backend: detected.KeyringBackend,
		Dir:     detected.KeyringDir,
	})
	if err != nil {
		fmt.Printf("❌ Failed to list keys: %v\n", err)
		return
	}

	if len(keys) == 0 {
		fmt.Println("No keys found in keyring.")
		return
	}

	fmt.Printf("Found %d key(s):\n\n", len(keys))
	for i, keyName := range keys {
		addr, err := getLocalAddressFromKeyring(config.KeyringConfig{
			Backend: detected.KeyringBackend,
			Dir:     detected.KeyringDir,
		}, keyName)

		status := ""
		if keyName == detected.KeyName {
			status = " (configured in supernode)"
		}

		fmt.Printf("%d. %s%s\n", i+1, keyName, status)
		if err == nil {
			fmt.Printf("   Address: %s\n", addr)
		} else {
			fmt.Printf("   Address: <error: %v>\n", err)
		}
		fmt.Println()
	}

	fmt.Println("Use -key-name <name> to specify which key to use.")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
