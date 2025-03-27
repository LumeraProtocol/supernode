package lumera

import "github.com/cosmos/cosmos-sdk/crypto/keyring"

// Option is a function that applies a change to Config
type Option func(*Config)

// WithGRPCAddr sets the gRPC endpoint address
func WithGRPCAddr(addr string) Option {
	return func(c *Config) {
		c.GRPCAddr = addr
	}
}

// WithChainID sets the chain ID
func WithChainID(chainID string) Option {
	return func(c *Config) {
		c.ChainID = chainID
	}
}

// WithTimeout sets the default timeout
func WithTimeout(seconds int) Option {
	return func(c *Config) {
		c.Timeout = seconds
	}
}

// WithKeyring sets the keyring conf for the node
func WithKeyring(k keyring.Keyring) Option {
	return func(c *Config) {
		c.keyring = k
	}
}
