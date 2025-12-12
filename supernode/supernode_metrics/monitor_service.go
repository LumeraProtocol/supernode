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

const (
	// Timing constants (behavioral, not chain params)
	DefaultStartupDelaySeconds = 30 // Delay before first report after startup
	PortCheckTimeoutSeconds    = 5  // Timeout for port accessibility checks

	// Port constants
	APIPort    = 4444 // API port
	P2PPort    = 4445 // P2P port
	StatusPort = 8002 // Status port
)

// Collector manages periodic metrics collection and reporting for the SuperNode.
type Collector struct {
	statusService *status.SupernodeStatusService
	lumeraClient  lumera.Client
	supernodeTx   supernode_msg.Module
	identity      string
	p2pClient     p2p.Client
	keyring       keyring.Keyring

	// Control
	stopChan chan struct{}
	wg       sync.WaitGroup

	// Configuration (derived from on-chain params)
	reportInterval time.Duration
	version        string
}

// NewCollector creates a new metrics collector instance.
func NewCollector(
	statusSvc *status.SupernodeStatusService,
	lumeraClient lumera.Client,
	identity string,
	version string,
	p2pClient p2p.Client,
	kr keyring.Keyring,
) *Collector {
	return &Collector{
		statusService: statusSvc,
		lumeraClient:  lumeraClient,
		supernodeTx:   lumeraClient.SuperNodeMsg(),
		identity:      strings.TrimSpace(identity),
		p2pClient:     p2pClient,
		keyring:       kr,
		stopChan:      make(chan struct{}),
		version:       version,
	}
}

// Start begins the metrics collection and reporting loop.
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

		params := paramsResp.GetParams()
		intervalBlocks := params.GetMetricsUpdateIntervalBlocks()
		if intervalBlocks == 0 {
			return fmt.Errorf("supernode params metrics_update_interval_blocks is zero or unset")
		}

		hm.reportInterval = hm.resolveReportInterval(ctx, intervalBlocks)
	}

	hm.wg.Add(1)
	go hm.reportingLoop(ctx)

	return nil
}

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
	// Start the collector
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
	// Collect metrics
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

	// Report to blockchain
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
