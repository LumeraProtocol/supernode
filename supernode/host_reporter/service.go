package host_reporter

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
	statussvc "github.com/LumeraProtocol/supernode/v2/supernode/status"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultDialTimeout  = 2 * time.Second
	defaultTickTimeout  = 30 * time.Second

	maxConcurrentTargets = 8
)

// Service submits one MsgSubmitEpochReport per epoch for the local supernode.
// All runtime behavior is driven by on-chain params/queries; there are no local config knobs.
type Service struct {
	identity string

	lumera  lumera.Client
	keyring keyring.Keyring
	keyName string

	pollInterval time.Duration
	dialTimeout  time.Duration

	metrics      *statussvc.MetricsCollector
	storagePaths []string
	p2pDataDir   string
}

func NewService(identity string, lumeraClient lumera.Client, kr keyring.Keyring, keyName string, baseDir string, p2pDataDir string) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.Audit() == nil || lumeraClient.AuditMsg() == nil || lumeraClient.SuperNode() == nil || lumeraClient.Node() == nil {
		return nil, fmt.Errorf("lumera client is missing required modules")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring is nil")
	}
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		return nil, fmt.Errorf("key name is empty")
	}

	// Defensive: ensure the configured identity matches the local signing key address.
	key, err := kr.Key(keyName)
	if err != nil {
		return nil, fmt.Errorf("keyring key not found: %w", err)
	}
	addr, err := key.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("get key address: %w", err)
	}
	if got := addr.String(); got != identity {
		return nil, fmt.Errorf("identity mismatch: config.identity=%s key(%s)=%s", identity, keyName, got)
	}

	storagePaths := []string{}
	p2pDataDir = strings.TrimSpace(p2pDataDir)
	if p2pDataDir != "" {
		// Everlight requirement: disk usage must reflect the mount/volume where p2p data is stored.
		storagePaths = []string{p2pDataDir}
	} else if baseDir = strings.TrimSpace(baseDir); baseDir != "" {
		// Fallback for legacy setups where p2p data dir isn't configured.
		storagePaths = []string{baseDir}
	}

	return &Service{
		identity:     identity,
		lumera:       lumeraClient,
		keyring:      kr,
		keyName:      keyName,
		pollInterval: defaultPollInterval,
		dialTimeout:  defaultDialTimeout,
		metrics:      statussvc.NewMetricsCollector(),
		storagePaths: storagePaths,
		p2pDataDir:   strings.TrimSpace(p2pDataDir),
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	// Bound each reporting cycle so a slow/hung chain RPC cannot block future ticks indefinitely.
	tickCtx, cancel := context.WithTimeout(ctx, defaultTickTimeout)
	defer cancel()

	epochResp, err := s.lumera.Audit().GetCurrentEpoch(tickCtx)
	if err != nil || epochResp == nil {
		return
	}
	epochID := epochResp.EpochId
	reachability.SetCurrentEpochID(epochID)

	anchorResp, err := s.lumera.Audit().GetEpochAnchor(tickCtx, epochID)
	if err != nil || anchorResp == nil || anchorResp.Anchor.EpochId != epochID {
		// Anchor may not be committed yet at the epoch boundary; retry on next tick.
		return
	}

	// Idempotency: if a report exists for this epoch, do nothing.
	if _, err := s.lumera.Audit().GetEpochReport(tickCtx, epochID, s.identity); err == nil {
		return
	} else if status.Code(err) != codes.NotFound {
		return
	}

	assignResp, err := s.lumera.Audit().GetAssignedTargets(tickCtx, s.identity, epochID)
	if err != nil || assignResp == nil {
		return
	}

	storageChallengeObservations := s.buildStorageChallengeObservations(tickCtx, epochID, assignResp.RequiredOpenPorts, assignResp.TargetSupernodeAccounts)

	hostReport := audittypes.HostReport{
		// Intentionally submit 0% usage for CPU/memory so the chain treats these as "unknown".
		// Disk usage is reported accurately (legacy-aligned) so disk-based enforcement can work.
		CpuUsagePercent: 0,
		MemUsagePercent: 0,
	}
	if diskUsagePercent, ok := s.diskUsagePercent(tickCtx); ok {
		hostReport.DiskUsagePercent = diskUsagePercent
	}
	if cascadeBytes, ok := s.cascadeKademliaDBBytes(tickCtx); ok {
		hostReport.CascadeKademliaDbBytes = float64(cascadeBytes)
	}

	if _, err := s.lumera.AuditMsg().SubmitEpochReport(tickCtx, epochID, hostReport, storageChallengeObservations); err != nil {
		logtrace.Warn(tickCtx, "epoch report submit failed", logtrace.Fields{
			"epoch_id": epochID,
			"error":    err.Error(),
		})
		return
	}

	logtrace.Info(tickCtx, "epoch report submitted", logtrace.Fields{
		"epoch_id":                             epochID,
		"storage_challenge_observations_count": len(storageChallengeObservations),
	})
}

func (s *Service) diskUsagePercent(ctx context.Context) (float64, bool) {
	if s.metrics == nil || len(s.storagePaths) == 0 {
		return 0, false
	}
	infos := s.metrics.CollectStorageMetrics(ctx, s.storagePaths)
	if len(infos) == 0 {
		return 0, false
	}
	return infos[0].UsagePercent, true
}

func (s *Service) cascadeKademliaDBBytes(_ context.Context) (uint64, bool) {
	dir := strings.TrimSpace(s.p2pDataDir)
	if dir == "" {
		return 0, false
	}
	// Kademlia SQLite store uses data*.sqlite3 files (+ WAL/SHM sidecars).
	matches, err := filepath.Glob(filepath.Join(dir, "data*.sqlite3*"))
	if err != nil || len(matches) == 0 {
		return 0, false
	}
	var total uint64
	for _, p := range matches {
		st, err := os.Stat(p)
		if err != nil || st == nil || st.IsDir() {
			continue
		}
		total += uint64(st.Size())
	}
	if total == 0 {
		return 0, false
	}
	return total, true
}

func (s *Service) buildStorageChallengeObservations(ctx context.Context, epochID uint64, requiredOpenPorts []uint32, targets []string) []*audittypes.StorageChallengeObservation {
	if len(targets) == 0 {
		return nil
	}

	out := make([]*audittypes.StorageChallengeObservation, len(targets))

	type workItem struct {
		index  int
		target string
	}

	work := make(chan workItem)
	done := make(chan struct{})

	worker := func() {
		defer func() { done <- struct{}{} }()
		for item := range work {
			out[item.index] = s.observeTarget(ctx, epochID, requiredOpenPorts, item.target)
		}
	}

	workers := maxConcurrentTargets
	if workers > len(targets) {
		workers = len(targets)
	}
	for i := 0; i < workers; i++ {
		go worker()
	}

	for i, t := range targets {
		work <- workItem{index: i, target: t}
	}
	close(work)

	for i := 0; i < workers; i++ {
		<-done
	}

	// ensure no nil elements (MsgSubmitEpochReport rejects nil observations)
	final := make([]*audittypes.StorageChallengeObservation, 0, len(out))
	for i := range out {
		if out[i] != nil {
			final = append(final, out[i])
		}
	}
	return final
}

func (s *Service) observeTarget(ctx context.Context, epochID uint64, requiredOpenPorts []uint32, target string) *audittypes.StorageChallengeObservation {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	host, err := s.targetHost(ctx, target)
	if err != nil {
		logtrace.Warn(ctx, "storage challenge observe target: resolve host failed", logtrace.Fields{
			"epoch_id": epochID,
			"target":   target,
			"error":    err.Error(),
		})
		host = ""
	}

	portStates := make([]audittypes.PortState, 0, len(requiredOpenPorts))
	for _, p := range requiredOpenPorts {
		portStates = append(portStates, probeTCP(ctx, host, p, s.dialTimeout))
	}

	return &audittypes.StorageChallengeObservation{
		TargetSupernodeAccount: target,
		PortStates:             portStates,
	}
}

func (s *Service) targetHost(ctx context.Context, supernodeAccount string) (string, error) {
	info, err := s.lumera.SuperNode().GetSupernodeWithLatestAddress(ctx, supernodeAccount)
	if err != nil || info == nil {
		return "", fmt.Errorf("resolve supernode address: %w", err)
	}
	raw := strings.TrimSpace(info.LatestAddress)
	if raw == "" {
		return "", fmt.Errorf("empty latest address for %s", supernodeAccount)
	}
	return normalizeProbeHost(raw), nil
}

func normalizeProbeHost(raw string) string {
	// LatestAddress is expected to be an IP/host, but tolerate host:port.
	if host, _, splitErr := net.SplitHostPort(raw); splitErr == nil && host != "" {
		return host
	}

	// Handle bracketed IPv6 literals without a port, e.g. "[2001:db8::1]".
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		if unbracketed := strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "["); unbracketed != "" {
			return unbracketed
		}
	}

	return raw
}

func probeTCP(ctx context.Context, host string, port uint32, timeout time.Duration) audittypes.PortState {
	host = strings.TrimSpace(host)
	if host == "" {
		return audittypes.PortState_PORT_STATE_UNKNOWN
	}
	if port == 0 || port > 65535 {
		return audittypes.PortState_PORT_STATE_UNKNOWN
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10)))
	if err != nil {
		return audittypes.PortState_PORT_STATE_CLOSED
	}
	_ = conn.Close()
	return audittypes.PortState_PORT_STATE_OPEN
}
