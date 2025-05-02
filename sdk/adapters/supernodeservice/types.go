package supernodeservice

import (
	"context"

	"google.golang.org/grpc"
)

type RegisterCascadeRequest struct {
	ActionID string
	TaskID   string
	FilePath string
}

type RegisterCascadeResponse struct {
	Success bool
	Message string
}

type CascadeServiceClient interface {
	RegisterCascade(ctx context.Context, in *RegisterCascadeRequest, opts ...grpc.CallOption) (*RegisterCascadeResponse, error)
}
