package net

import (
	"context"
	"fmt"

	"action/adapters/lumera"
	"action/log"

	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// FactoryConfig contains configuration for the ClientFactory
type FactoryConfig struct {
	LocalCosmosAddress   string
	DefaultSupernodePort int
}

// ClientFactory creates and manages supernode clients
type ClientFactory struct {
	logger        log.Logger
	keyring       keyring.Keyring
	clientOptions *client.ClientOptions
	config        FactoryConfig
}

// NewClientFactory creates a new client factory with the provided dependencies
func NewClientFactory(
	ctx context.Context,
	logger log.Logger,
	keyring keyring.Keyring,
	config FactoryConfig,
) *ClientFactory {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Debug(ctx, "Creating supernode client factory",
		"localAddress", config.LocalCosmosAddress,
		"defaultPort", config.DefaultSupernodePort)

	return &ClientFactory{
		logger:        logger,
		keyring:       keyring,
		clientOptions: client.DefaultClientOptions(),
		config:        config,
	}
}

// CreateClient creates a client for a specific supernode
func (f *ClientFactory) CreateClient(ctx context.Context, supernode lumera.Supernode) (SupernodeClient, error) {
	if supernode.GrpcEndpoint == "" {
		return nil, fmt.Errorf("supernode has no gRPC endpoint: %s", supernode.CosmosAddress)
	}

	// Ensure endpoint has port
	endpoint := AddPortIfMissing(supernode.GrpcEndpoint, f.config.DefaultSupernodePort)

	f.logger.Debug(ctx, "Creating supernode client",
		"supernode", supernode.CosmosAddress,
		"endpoint", endpoint)

	// Update the supernode with the properly formatted endpoint
	supernode.GrpcEndpoint = endpoint

	// Create client with dependencies
	client, err := NewSupernodeClient(
		ctx,
		f.logger,
		f.keyring,
		f.config.LocalCosmosAddress,
		supernode,
		f.clientOptions,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create supernode client for %s: %w",
			supernode.CosmosAddress, err)
	}

	return client, nil
}
