package supernode_metrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode_msg"
	"github.com/LumeraProtocol/supernode/v2/supernode/status"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
)

// Collector manages the end-to-end supernode metrics flow:
// 1) derive configuration from on-chain params,
// 2) collect local health data from the status service and helpers, and
// 3) periodically submit SupernodeMetrics reports back to the chain.
type Collector struct {
	// statusService is the single source of truth for local resource and
	// process health (CPU, memory, storage, uptime, peer count, etc.).
	statusService *status.SupernodeStatusService
	// lumeraClient is used both to fetch module params and to construct the
	// supernode message module used for ReportMetrics transactions.
	lumeraClient lumera.Client
	// supernodeTx exposes the Msg/tx API for submitting metrics to the chain.
	supernodeTx supernode_msg.Module
	// identity is the bech32 address of this supernode on-chain.
	identity string
	// p2pClient is reserved for future network-level health checks (P2P reachability).
	p2pClient p2p.Client
	// keyring holds the local signing key used when broadcasting metrics txs.
	keyring keyring.Keyring

	// Control
	// stopChan is closed to signal the reporting loop to exit.
	stopChan chan struct{}
	// wg tracks the lifetime of background goroutines to enable clean shutdowns.
	wg          sync.WaitGroup
	probePlanMu sync.RWMutex
	probePlan   *probePlan

	// Configuration (derived from on-chain params)
	// reportInterval is the wall-clock interval between metrics reports,
	// derived from the `metrics_update_interval_blocks` param and the observed block time.
	reportInterval              time.Duration
	metricsUpdateIntervalBlocks uint64
	metricsFreshnessMaxBlocks   uint64
	// version is the semantic version of this supernode binary, used to populate
	// the `version_*` fields in SupernodeMetrics.
	version string

	// Listener ports for this specific supernode instance.
	// These are used for populating `open_ports`.
	grpcPort    uint16
	p2pPort     uint16
	gatewayPort uint16
}

// NewCollector creates a new metrics collector instance.
func NewCollector(
	statusSvc *status.SupernodeStatusService,
	lumeraClient lumera.Client,
	identity string,
	version string,
	p2pClient p2p.Client,
	kr keyring.Keyring,
	grpcPort uint16,
	p2pPort uint16,
	gatewayPort uint16,
) *Collector {
	if grpcPort == 0 {
		grpcPort = APIPort
	}
	if p2pPort == 0 {
		p2pPort = P2PPort
	}
	if gatewayPort == 0 {
		gatewayPort = StatusPort
	}

	return &Collector{
		statusService: statusSvc,
		lumeraClient:  lumeraClient,
		supernodeTx:   lumeraClient.SuperNodeMsg(),
		identity:      strings.TrimSpace(identity),
		p2pClient:     p2pClient,
		keyring:       kr,
		stopChan:      make(chan struct{}),
		version:       version,
		grpcPort:      grpcPort,
		p2pPort:       p2pPort,
		gatewayPort:   gatewayPort,
	}
}

// Start initializes the collector's configuration (if needed) and launches the
// background reporting loop. It is safe to call only once.
func (hm *Collector) Start(ctx context.Context) error {
	if hm.reportInterval <= 0 {
		if hm.lumeraClient == nil {
			return fmt.Errorf("lumera client is not initialized")
		}

		paramsResp, err := hm.lumeraClient.SuperNode().GetParams(ctx)
		if err != nil || paramsResp == nil {
			logtrace.Error(ctx, fmt.Sprintf("failed to fetch supernode params for health monitor: %v", err), nil)
			return fmt.Errorf("failed to fetch supernode params for health monitor: %w", err)
		}

		params := paramsResp.GetParams().WithDefaults()
		intervalBlocks := params.GetMetricsUpdateIntervalBlocks()
		if intervalBlocks == 0 {
			return fmt.Errorf("supernode params metrics_update_interval_blocks is zero or unset")
		}

		hm.metricsUpdateIntervalBlocks = intervalBlocks
		hm.metricsFreshnessMaxBlocks = params.GetMetricsFreshnessMaxBlocks()
		hm.reportInterval = hm.resolveReportInterval(ctx, intervalBlocks)
	}

	hm.wg.Add(1)
	go hm.reportingLoop(ctx)

	// Active probing generates a small amount of deterministic peer-to-peer traffic so
	// that quiet-but-healthy nodes still receive inbound connections, enabling
	// evidence-based `open_ports` to converge to OPEN instead of UNKNOWN.
	hm.wg.Add(1)
	go hm.probingLoop(ctx)

	return nil
}

func (hm *Collector) probingEpochBlocks() uint64 {
	if hm == nil {
		return 0
	}
	return hm.metricsUpdateIntervalBlocks
}

func (hm *Collector) setProbePlan(plan *probePlan) {
	if hm == nil {
		return
	}
	hm.probePlanMu.Lock()
	hm.probePlan = plan
	hm.probePlanMu.Unlock()
}

func (hm *Collector) getProbePlan() *probePlan {
	if hm == nil {
		return nil
	}
	hm.probePlanMu.RLock()
	plan := hm.probePlan
	hm.probePlanMu.RUnlock()
	return plan
}

// resolveReportInterval converts a block-based interval into a wall-clock
// duration using the current estimated block time.
func (hm *Collector) resolveReportInterval(ctx context.Context, intervalBlocks uint64) time.Duration {
	if intervalBlocks == 0 {
		return 0
	}

	blockTime := hm.estimateBlockTime(ctx)
	if blockTime <= 0 {
		blockTime = time.Second
	}

	return time.Duration(intervalBlocks) * blockTime
}

// estimateBlockTime attempts to derive a realistic average block time by
// comparing the timestamps of the latest and previous blocks. If anything
// fails, a conservative 1s fallback is used.
func (hm *Collector) estimateBlockTime(ctx context.Context) time.Duration {
	const fallbackBlockTime = time.Second

	if hm.lumeraClient == nil {
		return fallbackBlockTime
	}

	nodeModule := hm.lumeraClient.Node()
	if nodeModule == nil {
		return fallbackBlockTime
	}

	latest, err := nodeModule.GetLatestBlock(ctx)
	if err != nil || latest == nil || latest.Block == nil {
		return fallbackBlockTime
	}

	height := latest.Block.Header.Height
	if height <= 1 {
		return fallbackBlockTime
	}

	prev, err := nodeModule.GetBlockByHeight(ctx, height-1)
	if err != nil || prev == nil || prev.Block == nil {
		return fallbackBlockTime
	}

	delta := latest.Block.Header.Time.Sub(prev.Block.Header.Time)
	if delta <= 0 {
		return fallbackBlockTime
	}

	return delta
}

// Run implements the service interface for use with RunServices.
func (hm *Collector) Run(ctx context.Context) error {
	// Start the collector and then block until the context is cancelled,
	// at which point we gracefully stop all background work.
	if err := hm.Start(ctx); err != nil {
		return err
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Gracefully stop
	hm.Stop()

	return nil
}

// Stop gracefully stops the collector.
func (hm *Collector) Stop() {
	// Closing stopChan signals the reporting loop to exit and allows the
	// WaitGroup to flush before returning.
	close(hm.stopChan)
	hm.wg.Wait()
}

// reportingLoop runs the periodic metrics reporting.
func (hm *Collector) reportingLoop(ctx context.Context) {
	defer hm.wg.Done()

	// Initial report after startup delay
	startupDelay := time.Duration(DefaultStartupDelaySeconds) * time.Second

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
			return
		case <-ctx.Done():
			return
		}
	}
}

// reportHealth collects and reports the current metrics snapshot.
func (hm *Collector) reportHealth(ctx context.Context) {
	// Collect metrics from local status/state and transform into the canonical
	// SupernodeMetrics shape required by the on-chain module.
	metrics, err := hm.collectMetrics(ctx)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to collect health metrics: %v", err), nil)
		return
	}

	logtrace.Info(ctx, "Reporting supernode metrics", logtrace.Fields{
		"identity":    hm.identity,
		"open_ports":  metrics.OpenPorts,
		"uptime_secs": metrics.UptimeSeconds,
	})

	// Report the metrics snapshot to the blockchain using the supernode
	// module's ReportMetrics Msg. Any failure is logged but does not panic
	// or stop the reporting loop.
	if err := hm.submitMetrics(ctx, metrics); err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to submit health metrics: %v", err), nil)
		return
	}
}

// submitMetrics sends metrics to the blockchain.
func (hm *Collector) submitMetrics(ctx context.Context, metrics sntypes.SupernodeMetrics) error {
	if hm.supernodeTx == nil {
		return fmt.Errorf("supernode tx module is not initialized")
	}

	identity := strings.TrimSpace(hm.identity)
	if identity == "" {
		return fmt.Errorf("supernode identity is not configured")
	}

	resp, err := hm.supernodeTx.ReportMetrics(ctx, identity, metrics)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to broadcast metrics transaction: %v", err), nil)
		return err
	}

	_ = resp
	logtrace.Info(ctx, "Metrics transaction broadcasted", logtrace.Fields{"identity": identity})

	return nil
}
