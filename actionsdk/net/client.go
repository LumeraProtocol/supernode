package net

import (
	"context"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type SupernodeClient interface {
	// UploadInputData uploads input data for cascade processing
	UploadInputData(ctx context.Context, in *cascade.UploadInputDataRequest, opts ...grpc.CallOption) (*cascade.UploadInputDataResponse, error)

	// HealthCheck performs a health check on the supernode
	HealthCheck(ctx context.Context) (*grpc_health_v1.HealthCheckResponse, error)

	Close() error
}
