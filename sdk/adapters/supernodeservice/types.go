package supernodeservice

import (
	"context"

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
    UptimeSeconds     uint64         // Uptime in seconds
    Resources struct {
        CPU struct {
            UsagePercent float64
            Cores        int32
        }
        Memory struct {
            TotalGB      float64
            UsedGB       float64
            AvailableGB  float64
            UsagePercent float64
        }
        Storage []StorageInfo
        HardwareSummary string // Formatted hardware summary
    }
    RunningTasks      []ServiceTasks // Services with running tasks
    RegisteredServices []string       // All available service names
    Network struct {
        PeersCount    int32    // Number of connected peers
        PeerAddresses []string // List of peer addresses
    }
    Rank      int32  // Rank in top supernodes list (0 if not in top list)
    IPAddress string // Supernode IP address with port
    // Optional detailed P2P metrics (present when requested)
    P2PMetrics struct {
        DhtMetrics struct {
            StoreSuccessRecent []struct {
                TimeUnix    int64
                Requests    int32
                Successful  int32
                SuccessRate float64
            }
            BatchRetrieveRecent []struct {
                TimeUnix     int64
                Keys         int32
                Required     int32
                FoundLocal   int32
                FoundNetwork int32
                DurationMS   int64
            }
            HotPathBannedSkips   int64
            HotPathBanIncrements int64
        }
        NetworkHandleMetrics map[string]struct{
            Total   int64
            Success int64
            Failure int64
            Timeout int64
        }
        ConnPoolMetrics map[string]int64
        BanList []struct {
            ID            string
            IP            string
            Port          uint32
            Count         int32
            CreatedAtUnix int64
            AgeSeconds    int64
        }
        Database struct {
            P2PDBSizeMB       float64
            P2PDBRecordsCount int64
        }
        Disk struct {
            AllMB  float64
            UsedMB float64
            FreeMB float64
        }
    }
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
