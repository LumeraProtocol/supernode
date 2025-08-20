package supernode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	snmodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

// Version is the supernode version, set by the main application
var Version = "dev"

// SupernodeStatusService provides centralized status information
// by collecting system metrics and aggregating task information from registered services
type SupernodeStatusService struct {
	taskProviders []TaskProvider    // List of registered services that provide task information
	metrics       *MetricsCollector // System metrics collector for CPU and memory stats
	storagePaths  []string          // Paths to monitor for storage metrics
	startTime     time.Time         // Service start time for uptime calculation
	p2pService    p2p.Client        // P2P service for network information
	lumeraClient  lumera.Client     // Lumera client for blockchain queries
	config        *config.Config    // Supernode configuration
}

// NewSupernodeStatusService creates a new supernode status service instance
func NewSupernodeStatusService(p2pService p2p.Client, lumeraClient lumera.Client, cfg *config.Config) *SupernodeStatusService {
	return &SupernodeStatusService{
		taskProviders: make([]TaskProvider, 0),
		metrics:       NewMetricsCollector(),
		storagePaths:  []string{"/"}, // Default to monitoring root filesystem
		startTime:     time.Now(),
		p2pService:    p2pService,
		lumeraClient:  lumeraClient,
		config:        cfg,
	}
}

// RegisterTaskProvider registers a service as a task provider
// This allows the service to report its running tasks in status responses
func (s *SupernodeStatusService) RegisterTaskProvider(provider TaskProvider) {
	s.taskProviders = append(s.taskProviders, provider)
}

// GetStatus returns the current system status including all registered services
// This method collects CPU metrics, memory usage, and task information from all providers
func (s *SupernodeStatusService) GetStatus(ctx context.Context) (StatusResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "GetStatus",
		logtrace.FieldModule: "SupernodeStatusService",
	}
	logtrace.Info(ctx, "status request received", fields)

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

	// Collect service information from all registered providers
	resp.RunningTasks = make([]ServiceTasks, 0, len(s.taskProviders))
	resp.RegisteredServices = make([]string, 0, len(s.taskProviders))

	for _, provider := range s.taskProviders {
		serviceName := provider.GetServiceName()
		tasks := provider.GetRunningTasks()

		// Add to registered services list
		resp.RegisteredServices = append(resp.RegisteredServices, serviceName)

		// Add all services to running tasks (even with 0 tasks)
		serviceTask := ServiceTasks{
			ServiceName: serviceName,
			TaskIDs:     tasks,
			TaskCount:   int32(len(tasks)),
		}
		resp.RunningTasks = append(resp.RunningTasks, serviceTask)
	}

	// Initialize network info
	resp.Network = NetworkInfo{
		PeersCount:    0,
		PeerAddresses: []string{},
	}

	// Collect P2P network information
	if s.p2pService != nil {
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
		}
	}

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

	// Set IP address from chain, fallback to external IP
	if s.config != nil && s.lumeraClient != nil {
		supernode, err := s.lumeraClient.SuperNode().GetSupernodeBySupernodeAddress(ctx, s.config.SupernodeConfig.Identity)
		if err == nil && supernode != nil {
			if latestIP, err := snmodule.GetLatestIP(supernode); err == nil {
				resp.IPAddress = latestIP
			}
		}

		if resp.IPAddress == "" {
			if externalIP, err := s.getExternalIP(ctx); err == nil {
				resp.IPAddress = fmt.Sprintf("%s:%d", externalIP, s.config.SupernodeConfig.Port)
			}
		}
	}

	// Log summary statistics
	totalTasks := 0
	for _, service := range resp.RunningTasks {
		totalTasks += int(service.TaskCount)
	}

	return resp, nil
}

// getExternalIP queries an external service to determine the public IP address
func (s *SupernodeStatusService) getExternalIP(ctx context.Context) (string, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get external IP: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Clean up the IP address
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "", fmt.Errorf("received empty IP address from external service")
	}

	return ip, nil
}
