package audit_reporter

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultPollInterval = 5 * time.Second
	defaultDialTimeout  = 2 * time.Second

	maxConcurrentTargets = 8
)

// Service submits one MsgSubmitAuditReport per epoch for the local supernode.
// All runtime behavior is driven by on-chain params/queries; there are no local config knobs.
type Service struct {
	identity string

	lumera  lumera.Client
	keyring keyring.Keyring
	keyName string

	pollInterval time.Duration
	dialTimeout  time.Duration
}

func NewService(identity string, lumeraClient lumera.Client, kr keyring.Keyring, keyName string) (*Service, error) {
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

	return &Service{
		identity:     identity,
		lumera:       lumeraClient,
		keyring:      kr,
		keyName:      keyName,
		pollInterval: defaultPollInterval,
		dialTimeout:  defaultDialTimeout,
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
	epochResp, err := s.lumera.Audit().GetCurrentEpoch(ctx)
	if err != nil || epochResp == nil {
		return
	}
	epochID := epochResp.EpochId
	reachability.SetCurrentEpochID(epochID)

	anchorResp, err := s.lumera.Audit().GetEpochAnchor(ctx, epochID)
	if err != nil || anchorResp == nil || anchorResp.Anchor.EpochId != epochID {
		// Anchor may not be committed yet at the epoch boundary; retry on next tick.
		return
	}

	// Idempotency: if a report exists for this epoch, do nothing.
	if _, err := s.lumera.Audit().GetAuditReport(ctx, epochID, s.identity); err == nil {
		return
	} else if status.Code(err) != codes.NotFound {
		return
	}

	assignResp, err := s.lumera.Audit().GetAssignedTargets(ctx, s.identity, epochID)
	if err != nil || assignResp == nil {
		return
	}

	peerObservations := s.buildPeerObservations(ctx, epochID, assignResp.RequiredOpenPorts, assignResp.TargetSupernodeAccounts)

	if _, err := s.lumera.AuditMsg().SubmitAuditReport(ctx, epochID, peerObservations); err != nil {
		logtrace.Warn(ctx, "audit report submit failed", logtrace.Fields{
			"epoch_id": epochID,
			"error":    err.Error(),
		})
		return
	}

	logtrace.Info(ctx, "audit report submitted", logtrace.Fields{
		"epoch_id":                epochID,
		"peer_observations_count": len(peerObservations),
	})
}

func (s *Service) buildPeerObservations(ctx context.Context, epochID uint64, requiredOpenPorts []uint32, targets []string) []*audittypes.AuditPeerObservation {
	if len(targets) == 0 {
		return nil
	}

	out := make([]*audittypes.AuditPeerObservation, len(targets))

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

	// ensure no nil elements (MsgSubmitAuditReport rejects nil observations)
	final := make([]*audittypes.AuditPeerObservation, 0, len(out))
	for i := range out {
		if out[i] != nil {
			final = append(final, out[i])
		}
	}
	return final
}

func (s *Service) observeTarget(ctx context.Context, epochID uint64, requiredOpenPorts []uint32, target string) *audittypes.AuditPeerObservation {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	host, err := s.targetHost(ctx, target)
	if err != nil {
		logtrace.Warn(ctx, "audit observe target: resolve host failed", logtrace.Fields{
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

	return &audittypes.AuditPeerObservation{
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

	// LatestAddress is expected to be an IP/host, but tolerate host:port.
	if host, _, splitErr := net.SplitHostPort(raw); splitErr == nil && host != "" {
		return host, nil
	}
	return raw, nil
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
