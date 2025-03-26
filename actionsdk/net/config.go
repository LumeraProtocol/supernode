package net

import (
	"action/adapters/lumera"
	"action/config"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Config holds configuration for creating a SupernodeClient
type Config struct {
	// Security configuration
	Keyring            keyring.Keyring
	LocalCosmosAddress string
	LocalPeerType      securekeyx.PeerType

	// Target supernode
	TargetSupernode lumera.Supernode

	// Default port if not specified in endpoint
	DefaultPort int

	// Client options
	ClientOptions *client.ClientOptions
}

// NewConfigFromGlobalConfig creates a client config from the global config
func NewConfigFromGlobalConfig(globalConfig config.Config, targetSupernode lumera.Supernode) *Config {
	return &Config{
		Keyring:            globalConfig.Keyring,
		LocalCosmosAddress: globalConfig.LocalCosmosAddress,
		LocalPeerType:      globalConfig.LocalPeerType,
		TargetSupernode:    targetSupernode,
		DefaultPort:        globalConfig.DefaultSupernodePort,
		ClientOptions:      client.DefaultClientOptions(),
	}
}

// DefaultClientOptions returns the default client options
func DefaultClientOptions() *client.ClientOptions {
	return client.DefaultClientOptions()
}
