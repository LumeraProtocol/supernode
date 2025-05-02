package config

import (
	"errors"
	"time"
)

const (
	DefaultLocalCosmosAddress = "lumera1qv3"     // Example address - replace with actual
	DefaultChainID            = "lumera-testnet" // Example chain ID - replace with actual
	DefaultGRPCAddr           = "127.0.0.1:9090"
	DefaultTimeout            = 10
)

// AccountConfig holds peer-to-peer addresses, ports, etc.
type AccountConfig struct {
	LocalCosmosAddress string // REQUIRED - cosmos account address used for signing tx
}

// LumeraConfig wraps all chain-specific dials.
type LumeraConfig struct {
	GRPCAddr string        // REQUIRED – e.g. "127.0.0.1:9090"
	ChainID  string        // REQUIRED – e.g. "lumera-mainnet"
	Timeout  time.Duration // OPTIONAL – defaults to DefaultTimeout
	KeyName  string
}

type Config struct {
	Account AccountConfig
	Lumera  LumeraConfig
}

// Option is a functional option for configuring the Config.
type Option func(*Config)

// New builds a Config, applying the supplied functional options.
func New(opts ...Option) (*Config, error) {
	cfg := &Config{
		Account: AccountConfig{LocalCosmosAddress: DefaultLocalCosmosAddress},
		Lumera: LumeraConfig{
			GRPCAddr: DefaultGRPCAddr,
			Timeout:  DefaultTimeout,
			ChainID:  DefaultChainID,
		},
	}

	for _, opt := range opts {
		opt(cfg)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func WithLocalCosmosAddress(addr string) Option {
	return func(c *Config) { c.Account.LocalCosmosAddress = addr }
}

func WithGRPCAddr(addr string) Option {
	return func(c *Config) { c.Lumera.GRPCAddr = addr }
}

func WithChainID(id string) Option {
	return func(c *Config) { c.Lumera.ChainID = id }
}

func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Lumera.Timeout = d }
}

// Validate checks the configuration for required fields and valid values.
func (c *Config) Validate() error {
	switch {
	case c.Account.LocalCosmosAddress == "":
		return errors.New("config: Network.LocalCosmosAddress is required")
	case c.Lumera.GRPCAddr == "":
		return errors.New("config: Lumera.GRPCAddr is required")
	case c.Lumera.ChainID == "":
		return errors.New("config: Lumera.ChainID is required")
	case c.Lumera.Timeout <= 0:
		return errors.New("config: Lumera.Timeout must be > 0")
	default:
		return nil
	}
}
