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

type SupernodeStatusresponse struct {
	CPU struct {
		Usage     string
		Remaining string
	}
	Memory struct {
		Total     uint64
		Used      uint64
		Available uint64
		UsedPerc  float64
	}
	TasksInProgress []string
}
type CascadeSupernodeDownloadRequest struct {
	ActionID    string
	TaskID      string
	OutputPath  string
	EventLogger LoggerFunc
}

type CascadeSupernodeDownloadResponse struct {
	Success    bool
	Message    string
	OutputPath string
}

//go:generate mockery --name=CascadeServiceClient --output=testutil/mocks --outpkg=mocks --filename=cascade_service_mock.go
type CascadeServiceClient interface {
	CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error)
	GetSupernodeStatus(ctx context.Context) (SupernodeStatusresponse, error)
	CascadeSupernodeDownload(ctx context.Context, in *CascadeSupernodeDownloadRequest, opts ...grpc.CallOption) (*CascadeSupernodeDownloadResponse, error)
}
