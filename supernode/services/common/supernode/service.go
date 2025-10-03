package supernode

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

// Version is the supernode version, set by the main application
var Version = "dev"

// SupernodeStatusService provides centralized status information
// by collecting system metrics and aggregating task information from registered services
type SupernodeStatusService struct {
	metrics      *MetricsCollector // System metrics collector for CPU and memory stats
	storagePaths []string          // Paths to monitor for storage metrics
	startTime    time.Time         // Service start time for uptime calculation
	p2pService   p2p.Client        // P2P service for network information
	lumeraClient lumera.Client     // Lumera client for blockchain queries
	config       *config.Config    // Supernode configuration
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService(p2pService p2p.Client, lumeraClient lumera.Client, cfg *config.Config) *SupernodeStatusService {
	return &SupernodeStatusService{
		metrics:      NewMetricsCollector(),
		storagePaths: []string{"/"}, // Default to monitoring root filesystem
		startTime:    time.Now(),
		p2pService:   p2pService,
		lumeraClient: lumeraClient,
		config:       cfg,
	}
}

// GetStatus returns the current system status including all registered services
// This method collects CPU metrics, memory usage, and task information from all providers
func (s *SupernodeStatusService) GetStatus(ctx context.Context, includeP2PMetrics bool) (StatusResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "GetStatus",
		logtrace.FieldModule: "SupernodeStatusService",
	}
	logtrace.Debug(ctx, "status request received", fields)

	var resp StatusResponse
	resp.Version = Version

	// Calculate uptime
	resp.UptimeSeconds = uint64(time.Since(s.startTime).Seconds())

	// Collect CPU metrics
	cpuUsage, err := s.metrics.CollectCPUMetrics(ctx)
	if err != nil {
		return resp, err
	}
	resp.Resources.CPU.UsagePercent = cpuUsage

	// Get CPU cores
	cpuCores, err := s.metrics.GetCPUCores(ctx)
	if err != nil {
		// Log error but continue - non-critical
		logtrace.Error(ctx, "failed to get cpu cores", logtrace.Fields{logtrace.FieldError: err.Error()})
		cpuCores = 0
	}
	resp.Resources.CPU.Cores = cpuCores

	// Collect memory metrics
	memTotal, memUsed, memAvailable, memUsedPerc, err := s.metrics.CollectMemoryMetrics(ctx)
	if err != nil {
		return resp, err
	}

	// Convert to GB
	const bytesToGB = 1024 * 1024 * 1024
	resp.Resources.Memory.TotalGB = float64(memTotal) / bytesToGB
	resp.Resources.Memory.UsedGB = float64(memUsed) / bytesToGB
	resp.Resources.Memory.AvailableGB = float64(memAvailable) / bytesToGB
	resp.Resources.Memory.UsagePercent = memUsedPerc

	// Generate hardware summary
	if cpuCores > 0 && resp.Resources.Memory.TotalGB > 0 {
		resp.Resources.HardwareSummary = fmt.Sprintf("%d cores / %.0fGB RAM", cpuCores, resp.Resources.Memory.TotalGB)
	}

	// Collect storage metrics
	resp.Resources.Storage = s.metrics.CollectStorageMetrics(ctx, s.storagePaths)

	// Task tracking removed; RegisteredServices populated at server layer

	// Initialize network info
	resp.Network = NetworkInfo{
		PeersCount:    0,
		PeerAddresses: []string{},
	}

	// Prepare P2P metrics container (always present in response)
	metrics := P2PMetrics{
		NetworkHandleMetrics: map[string]HandleCounters{},
		ConnPoolMetrics:      map[string]int64{},
		BanList:              []BanEntry{},
	}

	// Collect P2P network information and metrics (fill when available and requested)
	if includeP2PMetrics && s.p2pService != nil {
		p2pStats, err := s.p2pService.Stats(ctx)
		if err != nil {
			// Log error but continue - non-critical
			logtrace.Error(ctx, "failed to get p2p stats", logtrace.Fields{logtrace.FieldError: err.Error()})
		} else {
			if dhtStats, ok := p2pStats["dht"].(map[string]interface{}); ok {
				if peersCount, ok := dhtStats["peers_count"].(int); ok {
					resp.Network.PeersCount = int32(peersCount)
				}

				// Extract peer addresses
				if peers, ok := dhtStats["peers"].([]*kademlia.Node); ok {
					resp.Network.PeerAddresses = make([]string, 0, len(peers))
					for _, peer := range peers {
						// Format peer address as "ID@IP:Port"
						peerAddr := fmt.Sprintf("%s@%s:%d", string(peer.ID), peer.IP, peer.Port)
						resp.Network.PeerAddresses = append(resp.Network.PeerAddresses, peerAddr)
					}
				} else {
					resp.Network.PeerAddresses = []string{}
				}
			}

			// Disk info
			if du, ok := p2pStats["disk-info"].(utils.DiskStatus); ok {
				metrics.Disk = DiskStatus{AllMB: du.All, UsedMB: du.Used, FreeMB: du.Free}
			} else if duPtr, ok := p2pStats["disk-info"].(*utils.DiskStatus); ok && duPtr != nil {
				metrics.Disk = DiskStatus{AllMB: duPtr.All, UsedMB: duPtr.Used, FreeMB: duPtr.Free}
			}

			// Ban list
			if bans, ok := p2pStats["ban-list"].([]kademlia.BanSnapshot); ok {
				for _, b := range bans {
					metrics.BanList = append(metrics.BanList, BanEntry{
						ID:            b.ID,
						IP:            b.IP,
						Port:          uint32(b.Port),
						Count:         int32(b.Count),
						CreatedAtUnix: b.CreatedAt.Unix(),
						AgeSeconds:    int64(b.Age.Seconds()),
					})
				}
			}

			// Conn pool metrics
			if pool, ok := p2pStats["conn-pool"].(map[string]int64); ok {
				for k, v := range pool {
					metrics.ConnPoolMetrics[k] = v
				}
			}

			// DHT metrics and database/network counters live inside dht map
			if dhtStats, ok := p2pStats["dht"].(map[string]interface{}); ok {
				// Database
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
					metrics.Database = DatabaseStats{P2PDBSizeMB: sizeMB, P2PDBRecordsCount: recs}
				}

				// Network handle metrics
				if nhm, ok := dhtStats["network"].(map[string]kademlia.HandleCounters); ok {
					for k, c := range nhm {
						metrics.NetworkHandleMetrics[k] = HandleCounters{Total: c.Total, Success: c.Success, Failure: c.Failure, Timeout: c.Timeout}
					}
				} else if nhmI, ok := dhtStats["network"].(map[string]interface{}); ok {
					for k, vi := range nhmI {
						if c, ok := vi.(kademlia.HandleCounters); ok {
							metrics.NetworkHandleMetrics[k] = HandleCounters{Total: c.Total, Success: c.Success, Failure: c.Failure, Timeout: c.Timeout}
						}
					}
				}

				// Detailed recent per-request lists removed from API mapping
			}

			// DHT rolling metrics snapshot is attached at top-level under dht_metrics
			if snap, ok := p2pStats["dht_metrics"].(kademlia.DHTMetricsSnapshot); ok {
				// Store success
				for _, p := range snap.StoreSuccessRecent {
					metrics.DhtMetrics.StoreSuccessRecent = append(metrics.DhtMetrics.StoreSuccessRecent, StoreSuccessPoint{
						TimeUnix:    p.Time.Unix(),
						Requests:    int32(p.Requests),
						Successful:  int32(p.Successful),
						SuccessRate: p.SuccessRate,
					})
				}
				// Batch retrieve
				for _, p := range snap.BatchRetrieveRecent {
					metrics.DhtMetrics.BatchRetrieveRecent = append(metrics.DhtMetrics.BatchRetrieveRecent, BatchRetrievePoint{
						TimeUnix:     p.Time.Unix(),
						Keys:         int32(p.Keys),
						Required:     int32(p.Required),
						FoundLocal:   int32(p.FoundLocal),
						FoundNetwork: int32(p.FoundNet),
						DurationMS:   p.Duration.Milliseconds(),
					})
				}
				metrics.DhtMetrics.HotPathBannedSkips = snap.HotPathBannedSkips
				metrics.DhtMetrics.HotPathBanIncrements = snap.HotPathBanIncrements
			}
		}
	}

	// Always include metrics (may be empty if not available)
	resp.P2PMetrics = metrics

	// Calculate rank from top supernodes
	if s.lumeraClient != nil && s.config != nil {
		// Get current block height
		blockInfo, err := s.lumeraClient.Node().GetLatestBlock(ctx)
		if err != nil {
			// Log error but continue - non-critical
			logtrace.Error(ctx, "failed to get latest block", logtrace.Fields{logtrace.FieldError: err.Error()})
		} else {
			// Get top supernodes for current block
			topNodes, err := s.lumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, uint64(blockInfo.SdkBlock.Header.Height))
			if err != nil {
				// Log error but continue - non-critical
				logtrace.Error(ctx, "failed to get top supernodes", logtrace.Fields{logtrace.FieldError: err.Error()})
			} else {
				// Find our rank
				for idx, node := range topNodes.Supernodes {
					if node.SupernodeAccount == s.config.SupernodeConfig.Identity {
						resp.Rank = int32(idx + 1) // Rank starts from 1
						break
					}
				}
			}
		}
	}

	if s.config != nil && s.lumeraClient != nil {
		if supernodeInfo, err := s.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, s.config.SupernodeConfig.Identity); err == nil && supernodeInfo != nil {
			resp.IPAddress = supernodeInfo.LatestAddress
		}

	}

	return resp, nil
}
