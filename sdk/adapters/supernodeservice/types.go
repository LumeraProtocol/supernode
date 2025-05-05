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

//go:generate mockery --name=CascadeServiceClient --output=testutil/mocks --outpkg=mocks --filename=cascade_service_mock.go
type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
}
