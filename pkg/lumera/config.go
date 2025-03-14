package lumera

import "github.com/cosmos/cosmos-sdk/crypto/keyring"

// Config holds all the configuration needed for the client
type Config struct {
	// GRPCAddr is the gRPC endpoint address
	GRPCAddr string

	// ChainID is the ID of the chain
	ChainID string

	// Timeout is the default request timeout in seconds
	Timeout int

	// keyring is the keyring conf for the node sign & verify
	keyring keyring.Keyring
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		GRPCAddr: "localhost:9090",
		ChainID:  "lumera",
		Timeout:  10,
	}
}
