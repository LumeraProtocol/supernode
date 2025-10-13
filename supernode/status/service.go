package status

import (
    "context"
    "fmt"
    "time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/task"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

// Version is the supernode version, set by the main application
var Version = "dev"

const statusSubsystemTimeout = 8 * time.Second

// SupernodeStatusService provides centralized status information
type SupernodeStatusService struct {
	metrics      *MetricsCollector
	storagePaths []string
	startTime    time.Time
	p2pService   p2p.Client
	lumeraClient lumera.Client
	config       *config.Config
	tracker      task.Tracker
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService(p2pService p2p.Client, lumeraClient lumera.Client, cfg *config.Config, tracker task.Tracker) *SupernodeStatusService {
	return &SupernodeStatusService{metrics: NewMetricsCollector(), storagePaths: []string{"/"}, startTime: time.Now(), p2pService: p2pService, lumeraClient: lumeraClient, config: cfg, tracker: tracker}
}

// GetChainID returns the chain ID from the configuration
func (s *SupernodeStatusService) GetChainID() string {
	if s.config != nil {
		return s.config.LumeraClientConfig.ChainID
	}
	return ""
}

// GetStatus returns the current system status including optional P2P info
func (s *SupernodeStatusService) GetStatus(ctx context.Context, includeP2PMetrics bool) (*pb.StatusResponse, error) {
	fields := logtrace.Fields{logtrace.FieldMethod: "GetStatus", logtrace.FieldModule: "SupernodeStatusService"}
	logtrace.Debug(ctx, "status request received", fields)

	resp := &pb.StatusResponse{}
	resp.Version = Version
	resp.UptimeSeconds = uint64(time.Since(s.startTime).Seconds())

	cpuUsage, err := s.metrics.CollectCPUMetrics(ctx)
	if err != nil {
		return resp, err
	}
	if resp.Resources == nil {
		resp.Resources = &pb.StatusResponse_Resources{}
	}
	if resp.Resources.Cpu == nil {
		resp.Resources.Cpu = &pb.StatusResponse_Resources_CPU{}
	}
	resp.Resources.Cpu.UsagePercent = cpuUsage
	cores, err := s.metrics.GetCPUCores(ctx)
	if err != nil {
		logtrace.Error(ctx, "failed to get cpu cores", logtrace.Fields{logtrace.FieldError: err.Error()})
		cores = 0
	}
	resp.Resources.Cpu.Cores = cores
	memTotal, memUsed, memAvail, memUsedPerc, err := s.metrics.CollectMemoryMetrics(ctx)
	if err != nil {
		return resp, err
	}
	const bytesToGB = 1024 * 1024 * 1024
	if resp.Resources.Memory == nil {
		resp.Resources.Memory = &pb.StatusResponse_Resources_Memory{}
	}
	resp.Resources.Memory.TotalGb = float64(memTotal) / bytesToGB
	resp.Resources.Memory.UsedGb = float64(memUsed) / bytesToGB
	resp.Resources.Memory.AvailableGb = float64(memAvail) / bytesToGB
	resp.Resources.Memory.UsagePercent = memUsedPerc
	if cores > 0 && resp.Resources.Memory.TotalGb > 0 {
		resp.Resources.HardwareSummary = fmt.Sprintf("%d cores / %.0fGB RAM", cores, resp.Resources.Memory.TotalGb)
	}
	// Storage metrics
	for _, si := range s.metrics.CollectStorageMetrics(ctx, s.storagePaths) {
		resp.Resources.StorageVolumes = append(resp.Resources.StorageVolumes, &pb.StatusResponse_Resources_Storage{
			Path:           si.Path,
			TotalBytes:     si.TotalBytes,
			UsedBytes:      si.UsedBytes,
			AvailableBytes: si.AvailableBytes,
			UsagePercent:   si.UsagePercent,
		})
	}

	if resp.Network == nil {
		resp.Network = &pb.StatusResponse_Network{}
	}
	resp.Network.PeersCount = 0
	resp.Network.PeerAddresses = []string{}

	// Populate running tasks from injected tracker
	if s.tracker != nil {
		snap := s.tracker.Snapshot()
		if len(snap) > 0 {
			for svc, ids := range snap {
				resp.RunningTasks = append(resp.RunningTasks, &pb.StatusResponse_ServiceTasks{
					ServiceName: svc,
					TaskIds:     ids,
					TaskCount:   int32(len(ids)),
				})
			}
		}
	}

	// Prepare optional P2P metrics container
	pm := &pb.StatusResponse_P2PMetrics{
		DhtMetrics:           &pb.StatusResponse_P2PMetrics_DhtMetrics{},
		NetworkHandleMetrics: map[string]*pb.StatusResponse_P2PMetrics_HandleCounters{},
		ConnPoolMetrics:      map[string]int64{},
		BanList:              []*pb.StatusResponse_P2PMetrics_BanEntry{},
		Database:             &pb.StatusResponse_P2PMetrics_DatabaseStats{},
		Disk:                 &pb.StatusResponse_P2PMetrics_DiskStatus{},
	}

    if includeP2PMetrics && s.p2pService != nil {
        // Bound P2P metrics collection so status can't hang if P2P is slow
        p2pCtx, cancel := context.WithTimeout(ctx, statusSubsystemTimeout)
        defer cancel()
        p2pStats, err := s.p2pService.Stats(p2pCtx)
		if err != nil {
			logtrace.Error(ctx, "failed to get p2p stats", logtrace.Fields{logtrace.FieldError: err.Error()})
		} else {
			if dhtStats, ok := p2pStats["dht"].(map[string]interface{}); ok {
				if peersCount, ok := dhtStats["peers_count"].(int); ok {
					resp.Network.PeersCount = int32(peersCount)
				}
				if peers, ok := dhtStats["peers"].([]*kademlia.Node); ok {
					resp.Network.PeerAddresses = make([]string, 0, len(peers))
					for _, peer := range peers {
						resp.Network.PeerAddresses = append(resp.Network.PeerAddresses, fmt.Sprintf("%s@%s:%d", string(peer.ID), peer.IP, peer.Port))
					}
				} else {
					resp.Network.PeerAddresses = []string{}
				}
			}
			if du, ok := p2pStats["disk-info"].(utils.DiskStatus); ok {
				pm.Disk.AllMb = du.All
				pm.Disk.UsedMb = du.Used
				pm.Disk.FreeMb = du.Free
			} else if duPtr, ok := p2pStats["disk-info"].(*utils.DiskStatus); ok && duPtr != nil {
				pm.Disk.AllMb = duPtr.All
				pm.Disk.UsedMb = duPtr.Used
				pm.Disk.FreeMb = duPtr.Free
			}
			if bans, ok := p2pStats["ban-list"].([]kademlia.BanSnapshot); ok {
				for _, b := range bans {
					pm.BanList = append(pm.BanList, &pb.StatusResponse_P2PMetrics_BanEntry{Id: b.ID, Ip: b.IP, Port: uint32(b.Port), Count: int32(b.Count), CreatedAtUnix: b.CreatedAt.Unix(), AgeSeconds: int64(b.Age.Seconds())})
				}
			}
			if pool, ok := p2pStats["conn-pool"].(map[string]int64); ok {
				for k, v := range pool {
					pm.ConnPoolMetrics[k] = v
				}
			}
			if dhtStats, ok := p2pStats["dht"].(map[string]interface{}); ok {
				if db, ok := dhtStats["database"].(map[string]interface{}); ok {
					var sizeMB float64
					if v, ok := db["p2p_db_size"].(float64); ok {
						sizeMB = v
					}
					var recs int64
					switch v := db["p2p_db_records_count"].(type) {
					case int:
						recs = int64(v)
					case int64:
						recs = v
					case float64:
						recs = int64(v)
					}
					pm.Database.P2PDbSizeMb = sizeMB
					pm.Database.P2PDbRecordsCount = recs
				}
				if nhm, ok := dhtStats["network"].(map[string]kademlia.HandleCounters); ok {
					for k, c := range nhm {
						pm.NetworkHandleMetrics[k] = &pb.StatusResponse_P2PMetrics_HandleCounters{Total: c.Total, Success: c.Success, Failure: c.Failure, Timeout: c.Timeout}
					}
				} else if nhmI, ok := dhtStats["network"].(map[string]interface{}); ok {
					for k, vi := range nhmI {
						if c, ok := vi.(kademlia.HandleCounters); ok {
							pm.NetworkHandleMetrics[k] = &pb.StatusResponse_P2PMetrics_HandleCounters{Total: c.Total, Success: c.Success, Failure: c.Failure, Timeout: c.Timeout}
						}
					}
				}
			}
			if snap, ok := p2pStats["dht_metrics"].(kademlia.DHTMetricsSnapshot); ok {
				for _, sp := range snap.StoreSuccessRecent {
					pm.DhtMetrics.StoreSuccessRecent = append(pm.DhtMetrics.StoreSuccessRecent, &pb.StatusResponse_P2PMetrics_DhtMetrics_StoreSuccessPoint{TimeUnix: sp.Time.Unix(), Requests: int32(sp.Requests), Successful: int32(sp.Successful), SuccessRate: sp.SuccessRate})
				}
				for _, bp := range snap.BatchRetrieveRecent {
					pm.DhtMetrics.BatchRetrieveRecent = append(pm.DhtMetrics.BatchRetrieveRecent, &pb.StatusResponse_P2PMetrics_DhtMetrics_BatchRetrievePoint{TimeUnix: bp.Time.Unix(), Keys: int32(bp.Keys), Required: int32(bp.Required), FoundLocal: int32(bp.FoundLocal), FoundNetwork: int32(bp.FoundNet), DurationMs: bp.Duration.Milliseconds()})
				}
				pm.DhtMetrics.HotPathBannedSkips = snap.HotPathBannedSkips
				pm.DhtMetrics.HotPathBanIncrements = snap.HotPathBanIncrements
			}
		}
	}
	if includeP2PMetrics {
		resp.P2PMetrics = pm
	}

    if s.config != nil && s.lumeraClient != nil {
        // Bound chain query for latest address to avoid slow network hangs
        chainCtx, cancel := context.WithTimeout(ctx, statusSubsystemTimeout)
        defer cancel()
        if supernodeInfo, err := s.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(chainCtx, s.config.SupernodeConfig.Identity); err == nil && supernodeInfo != nil {
            resp.IpAddress = supernodeInfo.LatestAddress
        } else if err != nil {
            logtrace.Error(ctx, "failed to resolve latest supernode address", logtrace.Fields{logtrace.FieldError: err.Error()})
        }
    }
	return resp, nil
}
