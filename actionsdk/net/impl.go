package net

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type supernodeClient struct {
	cascadeClient cascade.CascadeServiceClient
	healthClient  grpc_health_v1.HealthClient
	conn          *grpc.ClientConn
}

// NewSupernodeClient creates a new supernode client
func NewSupernodeClient(ctx context.Context, config *Config) (SupernodeClient, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.Keyring == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}

	if config.LocalCosmosAddress == "" {
		return nil, fmt.Errorf("local cosmos address cannot be empty")
	}

	// Create client credentials
	clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       config.Keyring,
			LocalIdentity: config.LocalCosmosAddress,
			PeerType:      config.LocalPeerType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create credentials: %w", err)
	}

	// Create gRPC client
	grpcClient := client.NewClient(clientCreds)

	// Format address with identity for authentication
	targetGrpcEndpoint := config.TargetSupernode.GrpcEndpoint
	targetCosmosAddress := config.TargetSupernode.CosmosAddress
	addressWithIdentity := FormatAddressWithIdentity(targetCosmosAddress, targetGrpcEndpoint)

	// Connect to server
	conn, err := grpcClient.Connect(ctx, addressWithIdentity, config.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to supernode: %w", err)
	}

	// Create service clients
	return &supernodeClient{
		cascadeClient: cascade.NewCascadeServiceClient(conn),
		healthClient:  grpc_health_v1.NewHealthClient(conn),
		conn:          conn,
	}, nil
}

// UploadInputData sends data to the supernode for cascade processing
func (c *supernodeClient) UploadInputData(ctx context.Context, in *cascade.UploadInputDataRequest, opts ...grpc.CallOption) (*cascade.UploadInputDataResponse, error) {
	return c.cascadeClient.UploadInputData(ctx, in, opts...)
}

// HealthCheck performs a health check on the supernode
func (c *supernodeClient) HealthCheck(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error) {
	return c.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
}

// Close closes the connection to the supernode
func (c *supernodeClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
