package net

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/log"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// supernodeClient implements the SupernodeClient interface
type supernodeClient struct {
	cascadeClient supernodeservice.CascadeServiceClient
	healthClient  grpc_health_v1.HealthClient
	conn          *grpc.ClientConn
	logger        log.Logger
}

// Verify interface compliance at compile time
var _ SupernodeClient = (*supernodeClient)(nil)

// NewSupernodeClient creates a new supernode client
func NewSupernodeClient(ctx context.Context, logger log.Logger, keyring keyring.Keyring,
	factoryConfig FactoryConfig, targetSupernode lumera.Supernode, lumeraClient lumera.Client,
	clientOptions *client.ClientOptions,
) (SupernodeClient, error) {
	// Register ALTS protocols, just like in the test
	conn.RegisterALTSRecordProtocols()

	// Validate required parameters
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if keyring == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if factoryConfig.LocalCosmosAddress == "" {
		return nil, fmt.Errorf("local cosmos address cannot be empty")
	}

	if factoryConfig.PeerType == 0 {
		factoryConfig.PeerType = securekeyx.Simplenode
	}

	// Create client credentials
	clientCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       keyring,
			LocalIdentity: factoryConfig.LocalCosmosAddress,
			PeerType:      factoryConfig.PeerType,
			Validator:     lumeraClient,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create credentials: %w", err)
	}

	// Format connection address with identity for secure connection
	targetAddress := ltc.FormatAddressWithIdentity(
		targetSupernode.CosmosAddress,
		targetSupernode.GrpcEndpoint,
	)

	logger.Info(ctx, "Connecting to supernode securely", "endpoint", targetSupernode.GrpcEndpoint, "target_id", targetSupernode.CosmosAddress, "local_id", factoryConfig.LocalCosmosAddress, "peer_type", factoryConfig.PeerType)

	// Use provided client options or defaults
	options := clientOptions
	if options == nil {
		options = client.DefaultClientOptions()
	}

	// Connect to server with secure credentials
	grpcClient := client.NewClient(clientCreds)
	conn, err := grpcClient.Connect(ctx, targetAddress, options)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to supernode %s: %w",
			targetSupernode.CosmosAddress, err)
	}

	logger.Info(ctx, "Connected to supernode securely", "address", targetSupernode.CosmosAddress, "endpoint", targetSupernode.GrpcEndpoint)

	// Create service clients
	cascadeClient := supernodeservice.NewCascadeAdapter(
		ctx,
		conn,
		logger,
	)

	return &supernodeClient{
		cascadeClient: cascadeClient,
		healthClient:  grpc_health_v1.NewHealthClient(conn),
		conn:          conn,
		logger:        logger,
	}, nil
}

// RegisterCascade sends registration request to the supernode for cascade processing
func (c *supernodeClient) RegisterCascade(ctx context.Context, in *supernodeservice.CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*supernodeservice.CascadeSupernodeRegisterResponse, error) {
	resp, err := c.cascadeClient.CascadeSupernodeRegister(ctx, in, opts...)
	if err != nil {
		return nil, fmt.Errorf("cascade registration failed: %w", err)
	}

	c.logger.Info(ctx, "Cascade registered successfully",
		"actionID", in.ActionID, "taskId", in.TaskId)

	return resp, nil
}

// HealthCheck performs a health check on the supernode
func (c *supernodeClient) HealthCheck(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error) {
	resp, err := c.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}

	c.logger.Debug(ctx, "Health check completed", "status", resp.Status)
	return resp, nil
}

func (c *supernodeClient) GetSupernodeStatus(ctx context.Context) (*supernodeservice.SupernodeStatusresponse, error) {
	resp, err := c.cascadeClient.GetSupernodeStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode status: %w", err)
	}

	c.logger.Debug(ctx, "Supernode status retrieved successfully")
	return &resp, nil
}

// Download downloads the cascade action file
func (c *supernodeClient) Download(ctx context.Context, in *supernodeservice.CascadeSupernodeDownloadRequest, opts ...grpc.CallOption) (*supernodeservice.CascadeSupernodeDownloadResponse, error) {
	resp, err := c.cascadeClient.CascadeSupernodeDownload(ctx, in, opts...)
	if err != nil {
		return nil, fmt.Errorf("get artefacts failed: %w", err)
	}

	return resp, nil
}

// Close closes the connection to the supernode
func (c *supernodeClient) Close(ctx context.Context) error {
	if c.conn != nil {
		c.logger.Debug(ctx, "Closing connection to supernode")
		err := c.conn.Close()

		// Cleanup ALTS protocols when client is closed
		conn.UnregisterALTSRecordProtocols()

		return err
	}
	return nil
}
