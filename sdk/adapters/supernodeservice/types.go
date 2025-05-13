package supernodeservice

import (
	"context"

	"google.golang.org/grpc"

	"github.com/LumeraProtocol/supernode/sdk/event"
)

type LoggerFunc func(
	ctx context.Context,
	eventType event.EventType,
	message string,
	data map[string]interface{},
)

type CascadeSupernodeRegisterRequest struct {
	FilePath    string
	ActionID    string
	TaskId      string
	EventLogger LoggerFunc
}

type CascadeSupernodeRegisterResponse struct {
	Success bool
	Message string
	TxHash  string
}

//go:generate mockery --name=CascadeServiceClient --output=testutil/mocks --outpkg=mocks --filename=cascade_service_mock.go
type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
}
