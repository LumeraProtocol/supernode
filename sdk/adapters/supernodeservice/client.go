package supernodeservice

import (
	"context"

	"google.golang.org/grpc"
)

//go:generate mockgen -source=client.go -destination=mocks/client_mock.go -package=mocks
type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
}
