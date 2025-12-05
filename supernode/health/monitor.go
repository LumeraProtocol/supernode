package health

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/LumeraProtocol/supernode/v2/supernode/status"
)

// Health monitoring constants as per LEP-4 specification
const (
	// Timing constants
	DefaultReportIntervalMinutes = 90 // Default reporting interval in minutes (~1.5 hours)
	DefaultStartupDelaySeconds   = 30 // Delay before first report after startup
	PortCheckTimeoutSeconds      = 5  // Timeout for port accessibility checks

	// Port constants
	APIPort    = 4444 // API port
	P2PPort    = 4445 // P2P port
	StatusPort = 8002 // Status port

	// Metric key prefixes
	MetricVersionPrefix = "version"
	MetricCPUPrefix     = "cpu"
	MetricMemPrefix     = "mem"
	MetricDiskPrefix    = "disk"
	MetricPortPrefix    = "port"

	// Metric keys
	MetricVersionMajor     = "version.major"
	MetricVersionMinor     = "version.minor"
	MetricVersionPatch     = "version.patch"
	MetricCPUCoresTotal    = "cpu.cores_total"
	MetricCPUUsagePercent  = "cpu.usage_percent"
	MetricMemTotalGB       = "mem.total_gb"
	MetricMemFreeGB        = "mem.free_gb"
	MetricMemUsagePercent  = "mem.usage_percent"
	MetricDiskTotalGB      = "disk.total_gb"
	MetricDiskFreeGB       = "disk.free_gb"
	MetricDiskUsagePercent = "disk.usage_percent"
	MetricPort4444Open     = "port.4444_open"
	MetricPort4445Open     = "port.4445_open"
	MetricPort8002Open     = "port.8002_open"
	MetricUptimeSeconds    = "uptime_seconds"
	MetricPeersCount       = "peers.count"

	// Conversion constants
	BytesToGB = 1024.0 * 1024.0 * 1024.0

	// Default version if not specified
	DefaultVersionMajor = 2
	DefaultVersionMinor = 0
	DefaultVersionPatch = 0
)

// HealthMonitor manages health reporting for SuperNode
type HealthMonitor struct {
	statusService *status.SupernodeStatusService
	lumeraClient  lumera.Client
	config        *config.Config

	// Control
	stopChan chan struct{}
	wg       sync.WaitGroup

	// Configuration
	reportInterval time.Duration
	version        string
}

// NewHealthMonitor creates a new health monitor instance
func NewHealthMonitor(
	statusSvc *status.SupernodeStatusService,
	lumeraClient lumera.Client,
	cfg *config.Config,
	version string,
) *HealthMonitor {
	// Determine reporting interval
	reportInterval := time.Duration(DefaultReportIntervalMinutes) * time.Minute

	// Use config if specified, otherwise use default
	if cfg != nil && cfg.HealthConfig.ReportIntervalMinutes > 0 {
		reportInterval = time.Duration(cfg.HealthConfig.ReportIntervalMinutes) * time.Minute
	}

	// TODO: In future, get interval from blockchain params for dynamic adjustment

	return &HealthMonitor{
		statusService:  statusSvc,
		lumeraClient:   lumeraClient,
		config:         cfg,
		stopChan:       make(chan struct{}),
		reportInterval: reportInterval,
		version:        version,
	}
}

// Start begins the health monitoring and reporting loop
func (hm *HealthMonitor) Start(ctx context.Context) error {
	fields := logtrace.Fields{
		logtrace.FieldModule: "HealthMonitor",
		logtrace.FieldMethod: "Start",
	}

	fields["report_interval"] = hm.reportInterval.String()
	fields["version"] = hm.version
	logtrace.Info(ctx, "Starting health monitor", fields)

	hm.wg.Add(1)
	go hm.reportingLoop(ctx)

	return nil
}

// Run implements the service interface for use with RunServices
func (hm *HealthMonitor) Run(ctx context.Context) error {
	// Start the health monitor
	if err := hm.Start(ctx); err != nil {
		return err
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Gracefully stop
	hm.Stop()

	return nil
}

// Stop gracefully stops the health monitor
func (hm *HealthMonitor) Stop() {
	close(hm.stopChan)
	hm.wg.Wait()
}

// reportingLoop runs the periodic health reporting
func (hm *HealthMonitor) reportingLoop(ctx context.Context) {
	defer hm.wg.Done()

	fields := logtrace.Fields{
		logtrace.FieldModule: "HealthMonitor",
		logtrace.FieldMethod: "reportingLoop",
	}

	// Initial report after startup delay
	startupDelay := time.Duration(DefaultStartupDelaySeconds) * time.Second
	fields["delay"] = startupDelay.String()
	logtrace.Info(ctx, "Waiting for startup before first report", fields)

	select {
	case <-time.After(startupDelay):
		hm.reportHealth(ctx)
	case <-hm.stopChan:
		return
	case <-ctx.Done():
		return
	}

	// Regular reporting interval
	ticker := time.NewTicker(hm.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hm.reportHealth(ctx)
		case <-hm.stopChan:
			logtrace.Info(ctx, "Health monitor stopping", fields)
			return
		case <-ctx.Done():
			logtrace.Info(ctx, "Health monitor context cancelled", fields)
			return
		}
	}
}

// reportHealth collects and reports current health metrics
func (hm *HealthMonitor) reportHealth(ctx context.Context) {
	fields := logtrace.Fields{
		logtrace.FieldModule: "HealthMonitor",
		logtrace.FieldMethod: "reportHealth",
	}

	logtrace.Debug(ctx, "Collecting health metrics", fields)

	// Collect metrics
	metrics, err := hm.collectMetrics(ctx)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "Failed to collect metrics", fields)
		return
	}

	// Report to blockchain
	err = hm.submitMetrics(ctx, metrics)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "Failed to submit metrics", fields)
		return
	}

	fields["metrics_count"] = len(metrics)
	logtrace.Info(ctx, "Successfully reported health metrics", fields)
}

// collectMetrics gathers all health metrics in canonical format
func (hm *HealthMonitor) collectMetrics(ctx context.Context) (map[string]float64, error) {
	// Get status from the existing status service
	statusResp, err := hm.statusService.GetStatus(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}

	metrics := make(map[string]float64)

	// Version metrics
	versionParts := parseVersion(hm.version)
	metrics["version.major"] = float64(versionParts[0])
	metrics["version.minor"] = float64(versionParts[1])
	metrics["version.patch"] = float64(versionParts[2])

	// CPU metrics
	if statusResp.Resources != nil && statusResp.Resources.Cpu != nil {
		metrics["cpu.cores_total"] = float64(statusResp.Resources.Cpu.Cores)
		metrics["cpu.usage_percent"] = statusResp.Resources.Cpu.UsagePercent
	}

	// Memory metrics
	if statusResp.Resources != nil && statusResp.Resources.Memory != nil {
		metrics["mem.total_gb"] = statusResp.Resources.Memory.TotalGb
		metrics["mem.free_gb"] = statusResp.Resources.Memory.AvailableGb
		metrics["mem.usage_percent"] = statusResp.Resources.Memory.UsagePercent

		// Calculate usage percentage if not provided
		if metrics["mem.usage_percent"] == 0 && metrics["mem.total_gb"] > 0 {
			used := metrics["mem.total_gb"] - metrics["mem.free_gb"]
			metrics["mem.usage_percent"] = (used / metrics["mem.total_gb"]) * 100
		}
	}

	// Storage metrics (use first volume)
	if statusResp.Resources != nil && len(statusResp.Resources.StorageVolumes) > 0 {
		storage := statusResp.Resources.StorageVolumes[0]
		const bytesToGB = 1024.0 * 1024.0 * 1024.0

		metrics["disk.total_gb"] = float64(storage.TotalBytes) / bytesToGB
		metrics["disk.free_gb"] = float64(storage.AvailableBytes) / bytesToGB
		metrics["disk.usage_percent"] = storage.UsagePercent

		// Calculate usage percentage if not provided
		if metrics["disk.usage_percent"] == 0 && storage.TotalBytes > 0 {
			used := storage.TotalBytes - storage.AvailableBytes
			metrics["disk.usage_percent"] = float64(used) / float64(storage.TotalBytes) * 100
		}
	}

	// Port availability checks
	metrics["port.4444_open"] = hm.checkPort(ctx, 4444) // API port
	metrics["port.4445_open"] = hm.checkPort(ctx, 4445) // P2P port
	metrics["port.8002_open"] = hm.checkPort(ctx, 8002) // Status port

	// Runtime metrics
	metrics["uptime_seconds"] = float64(statusResp.UptimeSeconds)
	if statusResp.Network != nil {
		metrics["peers.count"] = float64(statusResp.Network.PeersCount)
	}

	return metrics, nil
}

// checkPort verifies if a port is accessible from external network
func (hm *HealthMonitor) checkPort(ctx context.Context, port int) float64 {
	// Get the node's public IP
	ip := hm.getPublicIP(ctx)
	if ip == "" {
		// Can't determine IP, assume port is not accessible
		return 0.0
	}

	// Try to connect to the port
	address := fmt.Sprintf("%s:%d", ip, port)
	dialer := net.Dialer{Timeout: 5 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		// Port is not accessible
		return 0.0
	}
	conn.Close()

	// Port is accessible
	return 1.0
}

// getPublicIP determines the node's public IP address from chain registration
func (hm *HealthMonitor) getPublicIP(ctx context.Context) string {
	// First try to get from health config if explicitly set (for testing/override)
	if hm.config != nil && hm.config.HealthConfig.PublicIP != "" {
		return hm.config.HealthConfig.PublicIP
	}

	// Get our registered IP from the blockchain - this is the source of truth
	// The SuperNode must be registered on chain to operate, so this should always work
	snInfo, err := hm.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, hm.config.SupernodeConfig.Identity)
	if err == nil && snInfo != nil && snInfo.LatestAddress != "" {
		// Extract IP from "ip:port" format if present
		address := strings.TrimSpace(snInfo.LatestAddress)
		if idx := strings.Index(address, ":"); idx > 0 {
			return address[:idx]
		}
		return address
	}

	// If we can't get IP from chain, log error and return empty
	// This likely means the node isn't properly registered
	fields := logtrace.Fields{
		logtrace.FieldModule: "HealthMonitor",
		logtrace.FieldMethod: "getPublicIP",
	}
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
	}
	logtrace.Error(ctx, "Failed to get IP from chain registration", fields)

	return ""
}

// parseVersion extracts major, minor, patch from version string
func parseVersion(version string) [3]int {
	result := [3]int{2, 0, 0} // Default to 2.0.0

	// Clean version string
	version = strings.TrimPrefix(version, "v")
	if idx := strings.IndexAny(version, "-+"); idx != -1 {
		version = version[:idx]
	}

	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		if num, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = num
		}
	}

	return result
}

// submitMetrics sends metrics to the blockchain
func (hm *HealthMonitor) submitMetrics(ctx context.Context, metrics map[string]float64) error {
	// Get validator address from config
	validatorAddr := hm.config.SupernodeConfig.Identity
	// Note: SuperNode account will be retrieved from keyring or blockchain when needed
	supernodeAcct := ""

	if validatorAddr == "" {
		return fmt.Errorf("validator address not configured")
	}

	// Create the metrics report message
	// Note: This will be the actual message structure once the blockchain side is ready
	// For now, this is a placeholder that matches the LEP-4 spec
	reportData := map[string]interface{}{
		"validator_address": validatorAddr,
		"supernode_account": supernodeAcct,
		"metrics":           metrics,
	}

	fields := logtrace.Fields{
		logtrace.FieldModule: "HealthMonitor",
		logtrace.FieldMethod: "submitMetrics",
	}

	// TODO: Submit to blockchain once the message type is available
	// This will be implemented once the blockchain team has the MsgReportSupernodeMetrics ready
	fields["validator"] = validatorAddr
	fields["metrics"] = reportData
	logtrace.Info(ctx, "Metrics ready for submission", fields)

	// For now, we just log the metrics
	// When blockchain is ready, this will be:
	// msg := &lumera.MsgReportSupernodeMetrics{...}
	// resp, err := hm.lumeraClient.BroadcastTx(ctx, msg)

	return nil
}
