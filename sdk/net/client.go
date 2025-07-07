package net

import (
	"context"

	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// SupernodeClient defines the interface for communicating with supernodes
type SupernodeClient interface {
	RegisterCascade(ctx context.Context, in *supernodeservice.CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*supernodeservice.CascadeSupernodeRegisterResponse, error)

	// HealthCheck performs a health check on the supernode
	HealthCheck(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error)

	GetSupernodeStatus(ctx context.Context) (*supernodeservice.SupernodeStatusresponse, error)
	// Download downloads the cascade action file
	Download(ctx context.Context, in *supernodeservice.CascadeSupernodeDownloadRequest, opts ...grpc.CallOption) (*supernodeservice.CascadeSupernodeDownloadResponse, error)

	// Close releases resources used by the client
	Close(ctx context.Context) error
}
