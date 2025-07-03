package supernodeservice

import (
	"context"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc"

	"github.com/LumeraProtocol/supernode/sdk/event"
)

type LoggerFunc func(
	ctx context.Context,
	eventType event.EventType,
	message string,
	data event.EventData,
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

type SupernodeStatusresponse = *cascade.HealthCheckResponse

//go:generate mockery --name=CascadeServiceClient --output=testutil/mocks --outpkg=mocks --filename=cascade_service_mock.go
type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
	GetSupernodeStatus(ctx context.Context) (SupernodeStatusresponse, error)
}
