package supernodeservice

import (
	"context"

	"google.golang.org/grpc"
)

type CascadeSupernodeRegisterRequest struct {
	Data     []byte
	ActionID string
	TaskId   string
}

type CascadeSupernodeRegisterResponse struct {
	Success bool
	Message string
}

type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
}
