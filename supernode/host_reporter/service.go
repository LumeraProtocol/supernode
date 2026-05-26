package host_reporter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/chainerrors"
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

// ProofResultProvider supplies the LEP-6 storage proof results that the host
// reporter must include in MsgSubmitEpochReport for a given epoch. The storage
// challenge runtime (PR3) implements this; until then the field stays nil and
// the host reporter submits an empty storage_proof_results slice.
type ProofResultProvider interface {
	// CollectResults returns the storage proof results buffered for epochID and
	// clears the buffer. Implementations must be safe for concurrent use.
	// Returning nil or an empty slice is valid (no proofs produced this epoch).
	CollectResults(epochID uint64) []*audittypes.StorageProofResult
}

// ProofResultRequeuer is implemented by providers that can put drained results
// back if the host reporter decides not to submit the epoch report. This keeps
// the FULL-mode coverage guard from losing late-arriving results when it aborts
// a would-be chain-rejected partial report.
type ProofResultRequeuer interface {
	RequeueResults(epochID uint64, results []*audittypes.StorageProofResult)
}

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

	proofResultProviderMu sync.RWMutex
	proofResultProvider   ProofResultProvider
}

// SetProofResultProvider attaches a ProofResultProvider to be drained on each
// epoch report. Wiring happens in supernode/cmd/start.go after the storage
// challenge runtime is constructed. It is safe to call before or after Run.
func (s *Service) SetProofResultProvider(p ProofResultProvider) {
	s.proofResultProviderMu.Lock()
	defer s.proofResultProviderMu.Unlock()
	s.proofResultProvider = p
}

func (s *Service) getProofResultProvider() ProofResultProvider {
	s.proofResultProviderMu.RLock()
	defer s.proofResultProviderMu.RUnlock()
	return s.proofResultProvider
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

	var storageProofResults []*audittypes.StorageProofResult
	proofResultProvider := s.getProofResultProvider()
	if proofResultProvider != nil {
		storageProofResults = proofResultProvider.CollectResults(epochID)
		mode, modeOK := s.storageTruthEnforcementMode(tickCtx)
		if modeOK && mode == audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL {
			// FULL mode is the only mode where the chain enforces compound
			// storage-proof coverage (one RECENT + one OLD per assigned target).
			// See lumera x/audit/v1/keeper/msg_submit_epoch_report.go:143
			// (enforceCompoundStorageProofs := mode == FULL). If our local
			// drain doesn't satisfy that, we MUST skip this epoch and
			// requeue the partial rows so the next tick can try again with
			// a complete set.
			complete, reason := storageProofCoverageComplete(storageProofResults, assignResp.TargetSupernodeAccounts)
			if !complete {
				requeueProofResults(proofResultProvider, epochID, storageProofResults)
				logtrace.Warn(tickCtx, "epoch report skipped: incomplete FULL-mode storage proof coverage", logtrace.Fields{
					"epoch_id":         epochID,
					"assigned_targets": len(assignResp.TargetSupernodeAccounts),
					"proof_results":    len(storageProofResults),
					"reason":           reason,
				})
				return
			}
		} else if modeOK && len(assignResp.TargetSupernodeAccounts) > 0 && len(storageProofResults) == 0 {
			// SHADOW / SOFT / UNSPECIFIED: chain accepts empty StorageProofResults
			// (only FULL enforces compound coverage). Submitting the host /
			// peer-observation report is mandatory regardless — withholding it
			// would feed audit_missing_reports and risk self-postponement
			// (ConsecutiveEpochsToPostpone defaults to 1). The trade-off is
			// that a same-epoch idempotency window can cause late-arriving
			// proof rows to be rejected as duplicate; that is acceptable in
			// observational modes because SHADOW/SOFT proofs do not affect
			// scoring (LEP-6 PR286 review F1).
			logtrace.Info(tickCtx, "epoch report: submitting in non-FULL mode with empty LEP-6 proof rows", logtrace.Fields{
				"epoch_id":         epochID,
				"assigned_targets": len(assignResp.TargetSupernodeAccounts),
				"mode":             mode.String(),
			})
		}
	}

	hostReport := audittypes.HostReport{
		// Intentionally submit 0% usage for CPU/memory so the chain treats these as "unknown".
		// Disk usage is reported accurately (legacy-aligned) so disk-based enforcement can work.
		CpuUsagePercent: 0,
		MemUsagePercent: 0,
	}
	if diskUsagePercent, ok := s.diskUsagePercent(tickCtx); ok {
		hostReport.DiskUsagePercent = diskUsagePercent
	}
	// Final Lumera LEP-6 HostReport no longer carries Cascade Kademlia DB byte counters;
	// keep disk usage as the host-side enforcement metric and leave the local helper intact
	// for existing diagnostics/tests.

	if _, err := s.lumera.AuditMsg().SubmitEpochReport(tickCtx, epochID, hostReport, storageChallengeObservations, storageProofResults); err != nil {
		// LEP-6 PR286 review F2: CollectResults destructively drained the
		// proof buffer. On submit failure we MUST decide whether those rows
		// can ever be re-submitted:
		//   - chain duplicate (report for this epoch already accepted) →
		//     drained rows are stale; do not requeue, just log;
		//   - any other error (transient RPC / sequence / validation) →
		//     requeue so next tick can retry with the same proofs.
		if chainerrors.IsEpochReportDuplicate(err) {
			logtrace.Info(tickCtx, "epoch report submit returned chain duplicate; drained proof rows discarded", logtrace.Fields{
				"epoch_id":      epochID,
				"proof_results": len(storageProofResults),
			})
			return
		}
		requeueProofResults(proofResultProvider, epochID, storageProofResults)
		logtrace.Warn(tickCtx, "epoch report submit failed; drained proof rows requeued for next tick", logtrace.Fields{
			"epoch_id":      epochID,
			"proof_results": len(storageProofResults),
			"error":         err.Error(),
		})
		return
	}

	logtrace.Info(tickCtx, "epoch report submitted", logtrace.Fields{
		"epoch_id":                             epochID,
		"storage_challenge_observations_count": len(storageChallengeObservations),
		"storage_proof_results_count":          len(storageProofResults),
	})
}

func (s *Service) storageTruthEnforcementMode(ctx context.Context) (audittypes.StorageTruthEnforcementMode, bool) {
	paramsResp, err := s.lumera.Audit().GetParams(ctx)
	if err != nil || paramsResp == nil {
		return audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED, false
	}
	return paramsResp.Params.StorageTruthEnforcementMode, true
}

// requeueProofResults returns the drained proof rows to the provider's
// buffer when the host reporter has decided not to ship them this tick
// (e.g. FULL-mode incomplete coverage or submit failure). Providers that
// don't implement ProofResultRequeuer silently drop the rows — same
// semantics as before requeueing was added.
func requeueProofResults(provider ProofResultProvider, epochID uint64, results []*audittypes.StorageProofResult) {
	if provider == nil || len(results) == 0 {
		return
	}
	if requeuer, ok := provider.(ProofResultRequeuer); ok {
		requeuer.RequeueResults(epochID, results)
	}
}

func storageProofCoverageComplete(results []*audittypes.StorageProofResult, targets []string) (bool, string) {
	if len(targets) == 0 {
		return true, ""
	}
	type coverage struct{ recent, old int }
	byTarget := make(map[string]*coverage, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		byTarget[target] = &coverage{}
	}
	for _, result := range results {
		if result == nil {
			continue
		}
		cov := byTarget[result.TargetSupernodeAccount]
		if cov == nil {
			continue
		}
		switch result.BucketType {
		case audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT:
			cov.recent++
		case audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD:
			cov.old++
		}
	}
	for target, cov := range byTarget {
		if cov.recent != 1 || cov.old != 1 {
			return false, fmt.Sprintf("target %s has recent=%d old=%d; FULL requires exactly one each", target, cov.recent, cov.old)
		}
	}
	return true, ""
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

// probeTCP attempts a short TCP dial to (host, port) and maps the outcome to
// an audittypes.PortState.
//
// LEP-6 review M6 (Matee, 2026-05-06): pre-Wave-4 every dial error mapped to
// PORT_STATE_CLOSED, including transient operator-side faults (DNS
// resolution failure, EHOSTUNREACH, context cancellation). Reporting these
// as CLOSED told the chain "this peer's port is down" when in fact our
// reporter just couldn't resolve / route to it. Post-Wave-4:
//   - ECONNREFUSED → CLOSED (the canonical "port is closed" signal — TCP
//     stack got a RST from the peer's host, which means the host is up
//     but no process is listening).
//   - DNS error / EHOSTUNREACH / ENETUNREACH / ctx.Err() / dial timeout →
//     UNKNOWN with a structured WARN log so operators can see the noise.
//   - Anything else also maps to UNKNOWN (default-safe). UNKNOWN does not
//     contribute to scoring, so this errs on the side of "don't accuse the
//     peer when we are not sure".
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
	if err == nil {
		_ = conn.Close()
		return audittypes.PortState_PORT_STATE_OPEN
	}

	// Canonical CLOSED signal: RST from peer's host (host up, no listener).
	if errors.Is(err, syscall.ECONNREFUSED) {
		return audittypes.PortState_PORT_STATE_CLOSED
	}

	// Operator-side / network-fault classes — UNKNOWN with structured warn.
	switch {
	case errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		// fall through to UNKNOWN below
	default:
		// DNS errors are *net.DNSError; timeouts implement net.Error.Timeout().
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			// fall through
		} else {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// fall through
			}
		}
	}
	logtrace.Warn(ctx, "host_reporter: probeTCP unclassified or operator-side fault — reporting UNKNOWN", logtrace.Fields{
		"host":  host,
		"port":  port,
		"error": err.Error(),
	})
	return audittypes.PortState_PORT_STATE_UNKNOWN
}
