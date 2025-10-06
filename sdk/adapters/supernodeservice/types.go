package supernodeservice

import (
	"context"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"google.golang.org/grpc"

	"github.com/LumeraProtocol/supernode/v2/sdk/event"
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

// Use generated proto types directly for status
type CascadeSupernodeDownloadRequest struct {
	ActionID    string
	TaskID      string
	OutputPath  string
	Signature   string
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
	GetSupernodeStatus(ctx context.Context) (*pb.StatusResponse, error)
	CascadeSupernodeDownload(ctx context.Context, in *CascadeSupernodeDownloadRequest, opts ...grpc.CallOption) (*CascadeSupernodeDownloadResponse, error)
}
