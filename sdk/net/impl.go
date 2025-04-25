package net

import (
	"context"
	"fmt"
	"os"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/log"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
func NewSupernodeClient(
	ctx context.Context,
	logger log.Logger,
	keyring keyring.Keyring,
	localCosmosAddress string,
	targetSupernode lumera.Supernode,
	clientOptions *client.ClientOptions,
) (SupernodeClient, error) {
	// Validate required parameters
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	// We still keep the keyring check for future reference
	if keyring == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if localCosmosAddress == "" {
		return nil, fmt.Errorf("local cosmos address cannot be empty")
	}

	// Create client credentials
	/*
		clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
			CommonOptions: credentials.CommonOptions{
				Keyring:       keyring,
				LocalIdentity: localCosmosAddress,
				PeerType:      securekeyx.Supernode,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create credentials: %w", err)
		}
	*/

	// Format connection address without identity for insecure connection
	targetAddress := targetSupernode.GrpcEndpoint

	logger.Debug(ctx, "Connecting to supernode insecurely", "address", targetAddress)

	// Use provided client options or defaults
	// options := clientOptions
	// if options == nil {
	// 	options = client.DefaultClientOptions()
	// }

	// Connect to server with insecure credentials
	// grpcClient := client.NewClient(clientCreds)
	// conn, err := grpcClient.Connect(ctx, targetAddress, options)

	// Use provided client options or defaults
	options := clientOptions
	if options == nil {
		options = client.DefaultClientOptions()
	}

	// Direct insecure connection via the grpc client wrapper
	grpcClient := client.NewClient(insecure.NewCredentials())
	conn, err := grpcClient.Connect(ctx, targetAddress, options)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to supernode %s: %w",
			targetSupernode.CosmosAddress, err)
	}

	logger.Info(ctx, "Connected to supernode insecurely", "address", targetSupernode.CosmosAddress)

	// Create service clients
	cascadeClient := supernodeservice.NewCascadeAdapter(
		ctx,
		cascade.NewCascadeServiceClient(conn),
		logger,
	)

	return &supernodeClient{
		cascadeClient: cascadeClient,
		healthClient:  grpc_health_v1.NewHealthClient(conn),
		conn:          conn,
		logger:        logger,
	}, nil
}

// UploadInputData sends data to the supernode for cascade processing
func (c *supernodeClient) UploadInputData(
	ctx context.Context,
	in *supernodeservice.UploadInputDataRequest,
	opts ...grpc.CallOption,
) (*supernodeservice.UploadInputDataResponse, error) {
	// Get file info for logging
	fileInfo, err := os.Stat(in.FilePath)
	var fileSize int64
	if err != nil {
		c.logger.Warn(ctx, "Failed to get file stats",
			"filePath", in.FilePath,
			"error", err)
	} else {
		fileSize = fileInfo.Size()
	}

	c.logger.Debug(ctx, "Uploading input data",
		"actionID", in.ActionID,
		"filename", in.Filename,
		"filePath", in.FilePath,
		"fileSize", fileSize)

	resp, err := c.cascadeClient.UploadInputData(ctx, in, opts...)
	if err != nil {
		return nil, fmt.Errorf("upload input data failed: %w", err)
	}

	c.logger.Info(ctx, "Input data uploaded successfully",
		"actionID", in.ActionID,
		"filename", in.Filename,
		"filePath", in.FilePath)

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

// Close closes the connection to the supernode
func (c *supernodeClient) Close(ctx context.Context) error {
	if c.conn != nil {
		c.logger.Debug(ctx, "Closing connection to supernode")
		return c.conn.Close()
	}
	return nil
}
