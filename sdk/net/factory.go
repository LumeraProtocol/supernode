package net

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/log"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// FactoryConfig contains configuration for the ClientFactory
type FactoryConfig struct {
	LocalCosmosAddress string
}

// ClientFactory creates and manages supernode clients
type ClientFactory struct {
	logger        log.Logger
	keyring       keyring.Keyring
	clientOptions *client.ClientOptions
	config        FactoryConfig
	lumeraClient  lumera.Client
}

// NewClientFactory creates a new client factory with the provided dependencies
func NewClientFactory(ctx context.Context, logger log.Logger, keyring keyring.Keyring, lumeraClient lumera.Client, config FactoryConfig) *ClientFactory {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Debug(ctx, "Creating supernode client factory",
		"localAddress", config.LocalCosmosAddress)

	return &ClientFactory{
		logger:        logger,
		keyring:       keyring,
		clientOptions: client.DefaultClientOptions(),
		config:        config,
		lumeraClient:  lumeraClient,
	}
}

// CreateClient creates a client for a specific supernode
func (f *ClientFactory) CreateClient(ctx context.Context, supernode lumera.Supernode) (SupernodeClient, error) {
	if supernode.GrpcEndpoint == "" {
		return nil, fmt.Errorf("supernode has no gRPC endpoint: %s", supernode.CosmosAddress)
	}

	f.logger.Debug(ctx, "Creating supernode client",
		"supernode", supernode.CosmosAddress,
		"endpoint", supernode.GrpcEndpoint)

	// Create client with dependencies
	client, err := NewSupernodeClient(ctx, f.logger, f.keyring, f.config.LocalCosmosAddress, supernode, f.lumeraClient,
		f.clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create supernode client for %s: %w", supernode.CosmosAddress, err)
	}

	return client, nil
}
