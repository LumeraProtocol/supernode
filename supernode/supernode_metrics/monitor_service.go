package supernode_metrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
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
	// keyring holds the local signing key used when broadcasting metrics txs.
	keyring keyring.Keyring

	// Control
	// stopChan is closed to signal the reporting loop to exit.
	stopChan chan struct{}
	// wg tracks the lifetime of background goroutines to enable clean shutdowns.
	wg sync.WaitGroup
	// stopOnce ensures Stop() is idempotent (closing a closed channel panics).
	stopOnce    sync.Once
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

	// lastParamsRefreshUnix tracks the last time we successfully refreshed params.
	// Used only for diagnostics/logging.
	lastParamsRefreshUnix atomic.Int64
}

// NewCollector creates a new metrics collector instance.
func NewCollector(
	statusSvc *status.SupernodeStatusService,
	lumeraClient lumera.Client,
	identity string,
	version string,
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
		hm.initParamsWithFallback(ctx)
	}

	hm.wg.Add(1)
	go hm.reportingLoop(ctx)

	// Active probing generates a small amount of deterministic peer-to-peer traffic so
	// that quiet-but-healthy nodes still receive inbound connections, enabling
	// evidence-based `open_ports` to converge to OPEN instead of UNKNOWN.
	hm.wg.Add(1)
	go hm.probingLoop(ctx)

	// Periodically refresh params so governance changes can be picked up without
	// requiring a supernode restart. Refresh is best-effort: failures keep the
	// current cached configuration.
	hm.wg.Add(1)
	go hm.paramsRefreshLoop(ctx)

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

func (hm *Collector) initParamsWithFallback(ctx context.Context) {
	// Default to a safe fallback; if chain params are available we overwrite below.
	hm.metricsUpdateIntervalBlocks = FallbackMetricsUpdateIntervalBlocks
	hm.metricsFreshnessMaxBlocks = 0
	hm.reportInterval = hm.resolveReportInterval(ctx, hm.metricsUpdateIntervalBlocks)

	if hm.lumeraClient == nil || hm.lumeraClient.SuperNode() == nil {
		logtrace.Warn(ctx, "Metrics params unavailable; using fallback defaults", logtrace.Fields{
			"metrics_update_interval_blocks": hm.metricsUpdateIntervalBlocks,
		})
		return
	}

	paramsResp, err := hm.lumeraClient.SuperNode().GetParams(ctx)
	if err != nil || paramsResp == nil {
		logtrace.Warn(ctx, "Failed to fetch supernode params; using fallback defaults", logtrace.Fields{
			logtrace.FieldError:              fmt.Sprintf("%v", err),
			"metrics_update_interval_blocks": hm.metricsUpdateIntervalBlocks,
		})
		return
	}

	params := paramsResp.GetParams().WithDefaults()
	intervalBlocks := params.GetMetricsUpdateIntervalBlocks()
	if intervalBlocks == 0 {
		logtrace.Warn(ctx, "Invalid supernode params metrics_update_interval_blocks=0; using fallback defaults", logtrace.Fields{
			"metrics_update_interval_blocks": hm.metricsUpdateIntervalBlocks,
		})
		return
	}

	hm.metricsUpdateIntervalBlocks = intervalBlocks
	hm.metricsFreshnessMaxBlocks = params.GetMetricsFreshnessMaxBlocks()
	hm.reportInterval = hm.resolveReportInterval(ctx, intervalBlocks)
	hm.lastParamsRefreshUnix.Store(time.Now().Unix())
}

func (hm *Collector) refreshParams(ctx context.Context) bool {
	if hm == nil || hm.lumeraClient == nil || hm.lumeraClient.SuperNode() == nil {
		return false
	}

	paramsResp, err := hm.lumeraClient.SuperNode().GetParams(ctx)
	if err != nil || paramsResp == nil {
		logtrace.Debug(ctx, "Metrics params refresh failed", logtrace.Fields{logtrace.FieldError: fmt.Sprintf("%v", err)})
		return false
	}
	params := paramsResp.GetParams().WithDefaults()
	intervalBlocks := params.GetMetricsUpdateIntervalBlocks()
	if intervalBlocks == 0 {
		logtrace.Debug(ctx, "Metrics params refresh returned intervalBlocks=0; ignoring", nil)
		return false
	}

	changed := intervalBlocks != hm.metricsUpdateIntervalBlocks ||
		params.GetMetricsFreshnessMaxBlocks() != hm.metricsFreshnessMaxBlocks

	hm.metricsUpdateIntervalBlocks = intervalBlocks
	hm.metricsFreshnessMaxBlocks = params.GetMetricsFreshnessMaxBlocks()
	hm.reportInterval = hm.resolveReportInterval(ctx, intervalBlocks)
	hm.lastParamsRefreshUnix.Store(time.Now().Unix())

	return changed
}

func (hm *Collector) paramsRefreshLoop(ctx context.Context) {
	defer hm.wg.Done()

	// Reasonable cadence: params rarely change; refreshing hourly is enough.
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			changed := hm.refreshParams(refreshCtx)
			cancel()
			if changed {
				logtrace.Info(ctx, "Metrics params refreshed", logtrace.Fields{
					"metrics_update_interval_blocks": hm.metricsUpdateIntervalBlocks,
					"metrics_freshness_max_blocks":   hm.metricsFreshnessMaxBlocks,
					"report_interval":                hm.reportInterval.String(),
				})
			}
		case <-hm.stopChan:
			return
		case <-ctx.Done():
			return
		}
	}
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
	if err != nil || latest == nil {
		return fallbackBlockTime
	}

	var (
		height     int64
		latestTime time.Time
	)
	if sdkBlk := latest.GetSdkBlock(); sdkBlk != nil {
		h := sdkBlk.GetHeader()
		height = h.Height
		latestTime = h.Time
	} else if blk := latest.GetBlock(); blk != nil {
		height = blk.Header.Height
		latestTime = blk.Header.Time
	} else {
		return fallbackBlockTime
	}
	if height <= 1 {
		return fallbackBlockTime
	}

	prev, err := nodeModule.GetBlockByHeight(ctx, height-1)
	if err != nil || prev == nil {
		return fallbackBlockTime
	}

	var prevTime time.Time
	if sdkBlk := prev.GetSdkBlock(); sdkBlk != nil {
		prevTime = sdkBlk.GetHeader().Time
	} else if blk := prev.GetBlock(); blk != nil {
		prevTime = blk.Header.Time
	} else {
		return fallbackBlockTime
	}

	delta := latestTime.Sub(prevTime)
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
	// Closing stopChan signals the reporting/probing loops to exit and allows the
	// WaitGroup to flush before returning.
	hm.stopOnce.Do(func() { close(hm.stopChan) })
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

	// Regular reporting interval; allow dynamic interval updates when params refresh.
	ticker := time.NewTicker(hm.reportInterval)
	defer ticker.Stop()
	currentInterval := hm.reportInterval

	for {
		select {
		case <-ticker.C:
			// Best-effort: if params refresh updated the interval, reset the ticker.
			if hm.reportInterval > 0 && hm.reportInterval != currentInterval {
				currentInterval = hm.reportInterval
				ticker.Reset(currentInterval)
			}
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
	if hm.metricsUpdateIntervalBlocks > 0 {
		if height, ok := hm.latestBlockHeight(ctx); ok {
			epochID := uint64(height) / hm.metricsUpdateIntervalBlocks
			reachability.SetCurrentEpochID(epochID)
		}
	}

	// Collect metrics from local status/state and transform into the canonical
	// SupernodeMetrics shape required by the on-chain module.
	metrics, err := hm.collectMetrics(ctx)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to collect health metrics: %v", err), nil)
		return
	}

	// Log the complete metrics message before submission so operators can
	// inspect exactly what is being broadcast to the chain.
	fields := logtrace.Fields{"metrics": metrics}

	epochID := reachability.CurrentEpochID()
	probe := logtrace.Fields{"epoch": epochID}

	// Option A per-service epoch success booleans (no probe RPC/proto).
	if tr := reachability.DefaultEpochTracker(); tr != nil {
		probe["in"] = logtrace.Fields{
			"success": logtrace.Fields{
				"grpc":    tr.Seen(epochID, reachability.ServiceGRPC),
				"p2p":     tr.Seen(epochID, reachability.ServiceP2P),
				"gateway": tr.Seen(epochID, reachability.ServiceGateway),
			},
		}
	}
	if plan := hm.getProbePlan(); plan != nil && plan.epochID == epochID && plan.expectedInbound != nil {
		in, _ := probe["in"].(logtrace.Fields)
		if in == nil {
			in = logtrace.Fields{}
		}
		in["expected"] = plan.expectedInbound[strings.TrimSpace(hm.identity)]
		probe["in"] = in

		probe["k"] = ProbeAssignmentsPerEpoch
		probe["out"] = logtrace.Fields{
			"expected":  len(plan.outboundTargets),
			"attempted": plan.outAttempted.Load(),
			"success":   plan.outSuccess.Load(),
		}
		if plan.chainID != "" {
			probe["chain_id"] = plan.chainID
		}
		if plan.randomnessHex != "" {
			probe["epoch_randomness_hex"] = plan.randomnessHex
		}
	}
	fields["probe"] = probe

	allPortsOpen := true
	for _, ps := range metrics.OpenPorts {
		if ps.State != sntypes.PortState_PORT_STATE_OPEN {
			allPortsOpen = false
			break
		}
	}
	if allPortsOpen {
		logtrace.Info(ctx, "Reporting supernode metrics", fields)
	} else {
		logtrace.Warn(ctx, "Reporting supernode metrics (one or more ports not OPEN)", fields)
	}

	// Report the metrics snapshot to the blockchain using the supernode
	// module's ReportMetrics Msg. Any failure is logged but does not panic
	// or stop the reporting loop.
	// TODO(revert-after-testing): Submit CPU/RAM usage as 0 while keeping logs intact.
	// This is a temporary test shim and should be removed once metrics validation/testing is complete.
	metricsToSubmit := metrics
	metricsToSubmit.CpuUsagePercent = 0
	metricsToSubmit.MemUsagePercent = 0

	if err := hm.submitMetrics(ctx, metricsToSubmit); err != nil {
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

	_, err := hm.supernodeTx.ReportMetrics(ctx, identity, metrics)
	if err != nil {
		logtrace.Error(ctx, fmt.Sprintf("failed to broadcast metrics transaction: %v", err), nil)
		return err
	}
	logtrace.Debug(ctx, "Metrics transaction broadcasted", logtrace.Fields{"identity": identity})

	return nil
}
