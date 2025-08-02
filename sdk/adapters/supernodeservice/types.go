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

// ServiceTasks contains task information for a specific service
type ServiceTasks struct {
	ServiceName string
	TaskIDs     []string
	TaskCount   int32
}

// StorageInfo contains storage metrics for a specific path
type StorageInfo struct {
	Path           string
	TotalBytes     uint64
	UsedBytes      uint64
	AvailableBytes uint64
	UsagePercent   float64
}

type SupernodeStatusresponse struct {
	Version           string         // Supernode version
	Resources struct {
		CPU struct {
			UsagePercent float64
		}
		Memory struct {
			TotalBytes     uint64
			UsedBytes      uint64
			AvailableBytes uint64
			UsagePercent   float64
		}
		Storage []StorageInfo
	}
	RunningTasks      []ServiceTasks // Services with running tasks
	RegisteredServices []string       // All available service names
}
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
	GetSupernodeStatus(ctx context.Context) (SupernodeStatusresponse, error)
	CascadeSupernodeDownload(ctx context.Context, in *CascadeSupernodeDownloadRequest, opts ...grpc.CallOption) (*CascadeSupernodeDownloadResponse, error)
}
