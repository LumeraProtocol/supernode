package net

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"

	keyringpkg "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// FactoryConfig contains configuration for the ClientFactory
type FactoryConfig struct {
	KeyName  string
	PeerType securekeyx.PeerType
}

// ClientFactory creates and manages supernode clients
type ClientFactory struct {
	logger        log.Logger
	keyring       keyring.Keyring
	clientOptions *client.ClientOptions
	config        FactoryConfig
	lumeraClient  lumera.Client
	signerAddr    string
}

// NewClientFactory creates a new client factory with the provided dependencies
func NewClientFactory(ctx context.Context, logger log.Logger, keyring keyring.Keyring, lumeraClient lumera.Client, config FactoryConfig) (*ClientFactory, error) {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	addr, err := keyringpkg.GetAddress(keyring, config.KeyName)
	if err != nil {
		logger.Error(ctx, "failed to resolve signer address from keyring",
			map[string]interface{}{"key_name": config.KeyName, "error": err.Error()},
		)

		return nil, fmt.Errorf("resolve signer address from keyring: %w", err)
	}

	// Tuned for 1GB max files with 4MB chunks
	// Reduce in-flight memory by aligning windows and msg sizes to chunk size.
	opts := client.DefaultClientOptions()
	opts.MaxRecvMsgSize = 12 * 1024 * 1024 // 8MB: supports 4MB chunks + overhead
	opts.MaxSendMsgSize = 12 * 1024 * 1024 // 8MB: supports 4MB chunks + overhead
	// Increase per-stream window to provide headroom for first data chunk + events
	opts.InitialWindowSize = 12 * 1024 * 1024     // 8MB per-stream window
	opts.InitialConnWindowSize = 64 * 1024 * 1024 // 64MB per-connection window
	opts.Logger = logger

	return &ClientFactory{
		logger:        logger,
		keyring:       keyring,
		clientOptions: opts,
		config:        config,
		lumeraClient:  lumeraClient,
		signerAddr:    addr.String(),
	}, nil
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
	client, err := NewSupernodeClient(ctx, f.logger, f.keyring, f.config, supernode, f.lumeraClient,
		f.clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create supernode client for %s: %w", supernode.CosmosAddress, err)
	}

	return client, nil
}
