package net

import (
	"context"

	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// SupernodeClient defines the interface for communicating with supernodes
type SupernodeClient interface {
	//RegisterCascade uploads input data to Supernode for processing cascade request
	RegisterCascade(ctx context.Context, in *supernodeservice.RegisterCascadeRequest, opts ...grpc.CallOption) (*supernodeservice.RegisterCascadeResponse, error)

	// HealthCheck performs a health check on the supernode
	HealthCheck(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error)

	// Close releases resources used by the client
	Close(ctx context.Context) error
}
