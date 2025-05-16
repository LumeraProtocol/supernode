package config

import (
	"errors"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

const (
	DefaultChainID  = "lumera-testnet"
	DefaultGRPCAddr = "127.0.0.1:9090"
	defaultKeyName  = "default"
)

// AccountConfig holds peer-to-peer addresses, ports, etc.
type AccountConfig struct {
	LocalCosmosAddress string
	KeyName            string
	Keyring            cosmoskeyring.Keyring
}

// LumeraConfig wraps all chain-specific dials.
type LumeraConfig struct {
	GRPCAddr string
	ChainID  string
}

type Config struct {
	Account AccountConfig
	Lumera  LumeraConfig
}

// Default returns a fully initialized config with default values and an auto-generated keyring
func DefaultConfigWithKr() (Config, error) {
	cfg := Config{
		Lumera: LumeraConfig{
			GRPCAddr: DefaultGRPCAddr,
			ChainID:  DefaultChainID,
		},
	}

	return WithKeyring(cfg)
}

// WithKeyring ensures the config has a keyring, creating one if needed
func WithKeyring(cfg Config) (Config, error) {
	// Apply any defaults for zero values
	if cfg.Lumera.GRPCAddr == "" {
		cfg.Lumera.GRPCAddr = DefaultGRPCAddr
	}
	if cfg.Lumera.ChainID == "" {
		cfg.Lumera.ChainID = DefaultChainID
	}

	// Initialize Lumera SDK config
	keyring.InitSDKConfig()

	// If keyring is nil, create an in-memory keyring and generate a new account
	if cfg.Account.Keyring == nil {
		// Set default key name if not provided
		keyName := cfg.Account.KeyName
		if keyName == "" {
			keyName = defaultKeyName
		}

		// Create an in-memory keyring
		kr, err := keyring.InitKeyring("memory", "")
		if err != nil {
			return Config{}, fmt.Errorf("failed to create in-memory keyring: %w", err)
		}

		// Create a new account with generated mnemonic
		_, info, err := keyring.CreateNewAccount(kr, keyName)
		if err != nil {
			return Config{}, fmt.Errorf("failed to create new account: %w", err)
		}

		// Get the address of the new account
		addr, err := info.GetAddress()
		if err != nil {
			return Config{}, fmt.Errorf("failed to get address from account: %w", err)
		}

		// Update config with the new keyring and address
		cfg.Account.Keyring = kr
		cfg.Account.KeyName = keyName
		cfg.Account.LocalCosmosAddress = addr.String()
	}

	// Validate the config
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration for required fields and valid values.
func (c Config) Validate() error {
	switch {
	case c.Account.LocalCosmosAddress == "":
		return errors.New("config: Network.LocalCosmosAddress is required")
	case c.Lumera.GRPCAddr == "":
		return errors.New("config: Lumera.GRPCAddr is required")
	case c.Lumera.ChainID == "":
		return errors.New("config: Lumera.ChainID is required")
	default:
		return nil
	}
}
