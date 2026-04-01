package self_healing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	query "github.com/cosmos/cosmos-sdk/types/query"
	"lukechampine.com/blake3"
)

const (
	defaultPollInterval = 10 * time.Second

	defaultPingInterval        = 7 * time.Minute
	defaultWatchlistInterval   = 8 * time.Minute
	defaultGenerationInterval  = 60 * time.Minute
	defaultProcessEventsPeriod = 2 * time.Minute

	defaultWatchlistThreshold = 6
	defaultWatchlistStaleFor  = 20 * time.Minute
	defaultWatchlistFreshFor  = 60 * time.Minute
	defaultClosestNodes       = 6
	defaultReplicaCount       = 5
	defaultObserverCount      = 2
	defaultObserverThreshold  = 2
	defaultActionPageLimit    = 200
	defaultActionTargetsCache = 15 * time.Minute
	defaultMaxChallenges      = 2048
	defaultEventLeaseDuration = 2 * time.Minute
	defaultEventMaxAttempts   = 3
	defaultEventRetryBase     = 30 * time.Second
	defaultEventRetryMax      = 15 * time.Minute
	defaultMaxEventsPerTick   = 64
	defaultEventWorkers       = 8
	defaultDirectProbeTimeout = 3 * time.Second
	defaultDirectProbeWorkers = 8

	requestTimeout     = 20 * time.Second
	verificationTimout = 20 * time.Second
	commitTimeout      = 20 * time.Second
)

type Config struct {
	Enabled      bool
	PollInterval time.Duration
	KeyName      string

	PingInterval       time.Duration
	WatchlistInterval  time.Duration
	GenerationInterval time.Duration
	ProcessInterval    time.Duration

	WatchlistThreshold           int
	WatchlistStaleFor            time.Duration
	WatchlistFreshFor            time.Duration
	ClosestNodes                 int
	ObserverCount                int
	ObserverThreshold            int
	ActionPageLimit              int
	ActionTargetsTTL             time.Duration
	MaxChallenges                int
	EventLeaseDuration           time.Duration
	EventRetryBase               time.Duration
	EventRetryMax                time.Duration
	MaxEventAttempts             int
	MaxEventsPerTick             int
	EventWorkers                 int
	RequireDirectMissingEvidence bool
	DirectProbeTimeout           time.Duration
	DirectProbeWorkers           int

	// Max age for a generation window before we mark event stale/terminal.
	MaxWindowAge time.Duration
}

type Service struct {
	cfg      Config
	identity string
	grpcPort uint16

	lumera lumera.Client
	p2p    p2p.Client
	kr     keyring.Keyring
	store  queries.LocalStoreInterface

	grpcClient *grpcclient.Client
	grpcOpts   *grpcclient.ClientOptions

	requestSelfHealingFn func(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error)
	verifySelfHealingFn  func(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySelfHealingRequest, timeout time.Duration) (*supernode.VerifySelfHealingResponse, error)
	commitSelfHealingFn  func(ctx context.Context, remoteIdentity string, address string, req *supernode.CommitSelfHealingRequest, timeout time.Duration) (*supernode.CommitSelfHealingResponse, error)

	targetsCacheMu sync.RWMutex
	targetsCache   []actionHealingTarget
	targetsCached  time.Time

	lastPingAt       time.Time
	lastWatchlistAt  time.Time
	lastGenerateAt   time.Time
	lastProcessEvtAt time.Time
}

type challengeEventPayload struct {
	EpochID    uint64   `json:"epoch_id,omitempty"`
	WindowID   int64    `json:"window_id"`
	ActionID   string   `json:"action_id,omitempty"`
	FileKey    string   `json:"file_key"`
	Recipient  string   `json:"recipient"`
	Observers  []string `json:"observers"`
	WatchHash  string   `json:"watch_hash"`
	ActiveHash string   `json:"active_hash"`
}

type eventProcessError struct {
	Reason    string
	Retryable bool
	Err       error
}

func (e *eventProcessError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Reason, e.Err)
	}
	return e.Reason
}

func (e *eventProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewService(identity string, grpcPort uint16, lumeraClient lumera.Client, p2pClient p2p.Client, kr keyring.Keyring, store queries.LocalStoreInterface, cfg Config) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.SuperNode() == nil || lumeraClient.Audit() == nil || lumeraClient.Action() == nil {
		return nil, fmt.Errorf("lumera client is missing required modules")
	}
	if p2pClient == nil {
		return nil, fmt.Errorf("p2p client is nil")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("history store is nil")
	}
	if strings.TrimSpace(cfg.KeyName) == "" {
		return nil, fmt.Errorf("key name is empty")
	}
	key, err := kr.Key(cfg.KeyName)
	if err != nil {
		return nil, fmt.Errorf("keyring key not found: %w", err)
	}
	addr, err := key.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("get key address: %w", err)
	}
	if got := addr.String(); got != identity {
		return nil, fmt.Errorf("identity mismatch: config.identity=%s key(%s)=%s", identity, cfg.KeyName, got)
	}

	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = defaultPingInterval
	}
	if cfg.WatchlistInterval <= 0 {
		cfg.WatchlistInterval = defaultWatchlistInterval
	}
	if cfg.GenerationInterval <= 0 {
		cfg.GenerationInterval = defaultGenerationInterval
	}
	if cfg.ProcessInterval <= 0 {
		cfg.ProcessInterval = defaultProcessEventsPeriod
	}
	if cfg.WatchlistThreshold <= 0 {
		cfg.WatchlistThreshold = defaultWatchlistThreshold
	}
	if cfg.WatchlistStaleFor <= 0 {
		cfg.WatchlistStaleFor = defaultWatchlistStaleFor
	}
	if cfg.WatchlistFreshFor <= 0 {
		cfg.WatchlistFreshFor = defaultWatchlistFreshFor
	}
	if cfg.ClosestNodes <= 0 {
		cfg.ClosestNodes = defaultClosestNodes
	}
	if cfg.ObserverCount <= 0 {
		cfg.ObserverCount = defaultObserverCount
	}
	if cfg.ObserverThreshold <= 0 {
		cfg.ObserverThreshold = defaultObserverThreshold
	}
	if cfg.ActionPageLimit <= 0 {
		cfg.ActionPageLimit = defaultActionPageLimit
	}
	if cfg.ActionTargetsTTL <= 0 {
		cfg.ActionTargetsTTL = defaultActionTargetsCache
	}
	if cfg.MaxChallenges <= 0 {
		cfg.MaxChallenges = defaultMaxChallenges
	}
	if cfg.EventLeaseDuration <= 0 {
		cfg.EventLeaseDuration = defaultEventLeaseDuration
	}
	if cfg.EventRetryBase <= 0 {
		cfg.EventRetryBase = defaultEventRetryBase
	}
	if cfg.EventRetryMax <= 0 {
		cfg.EventRetryMax = defaultEventRetryMax
	}
	if cfg.MaxEventAttempts <= 0 {
		cfg.MaxEventAttempts = defaultEventMaxAttempts
	}
	if cfg.MaxEventsPerTick <= 0 {
		cfg.MaxEventsPerTick = defaultMaxEventsPerTick
	}
	if cfg.EventWorkers <= 0 {
		cfg.EventWorkers = defaultEventWorkers
	}
	if cfg.DirectProbeTimeout <= 0 {
		cfg.DirectProbeTimeout = defaultDirectProbeTimeout
	}
	if cfg.DirectProbeWorkers <= 0 {
		cfg.DirectProbeWorkers = defaultDirectProbeWorkers
	}
	if cfg.MaxWindowAge <= 0 {
		cfg.MaxWindowAge = 2 * cfg.GenerationInterval
	}

	return &Service{cfg: cfg, identity: identity, grpcPort: grpcPort, lumera: lumeraClient, p2p: p2pClient, kr: kr, store: store}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		<-ctx.Done()
		return nil
	}
	if err := s.initClients(); err != nil {
		return err
	}

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			now := time.Now().UTC()
			if now.Sub(s.lastPingAt) >= s.cfg.PingInterval {
				s.refreshPingInfo(ctx)
				s.lastPingAt = now
			}
			if now.Sub(s.lastWatchlistAt) >= s.cfg.WatchlistInterval {
				s.updateWatchlist(ctx)
				s.lastWatchlistAt = now
			}
			if now.Sub(s.lastGenerateAt) >= s.cfg.GenerationInterval {
				s.generateChallenges(ctx, now)
				s.lastGenerateAt = now
			}
			if now.Sub(s.lastProcessEvtAt) >= s.cfg.ProcessInterval {
				s.processEvents(ctx)
				s.lastProcessEvtAt = now
			}
		}
	}
}

func (s *Service) initClients() error {
	validator := lumera.NewSecureKeyExchangeValidator(s.lumera)
	grpcCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       s.kr,
			LocalIdentity: s.identity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return fmt.Errorf("create gRPC client creds: %w", err)
	}
	s.grpcClient = grpcclient.NewClient(grpcCreds)
	s.grpcOpts = grpcclient.DefaultClientOptions()
	s.grpcOpts.EnableRetries = true
	return nil
}

func (s *Service) refreshPingInfo(ctx context.Context) {
	active, _, addrMap, err := s.networkSnapshot(ctx)
	if err != nil {
		logtrace.Warn(ctx, "self-healing ping: snapshot failed", logtrace.Fields{"error": err.Error()})
		return
	}
	for _, id := range active {
		if id == s.identity {
			continue
		}
		addr := addrMap[id]
		online := s.probe(addr)
		s.upsertPing(ctx, id, addr, online)
	}
}

func (s *Service) probe(address string) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	host, port, ok := parseHostAndPort(address, int(s.grpcPort))
	if !ok {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *Service) upsertPing(ctx context.Context, nodeID, ip string, online bool) {
	existing, err := s.store.GetPingInfoBySupernodeID(nodeID)
	if err != nil && err != sql.ErrNoRows {
		return
	}
	if existing == nil {
		existing = &types.PingInfo{SupernodeID: nodeID, IPAddress: ip}
	}
	now := time.Now().UTC()
	pi := types.PingInfo{
		SupernodeID:            nodeID,
		IPAddress:              ip,
		TotalPings:             existing.TotalPings + 1,
		TotalSuccessfulPings:   existing.TotalSuccessfulPings,
		AvgPingResponseTime:    existing.AvgPingResponseTime,
		IsOnline:               online,
		IsOnWatchlist:          existing.IsOnWatchlist,
		IsAdjusted:             existing.IsAdjusted,
		CumulativeResponseTime: existing.CumulativeResponseTime,
		LastSeen:               existing.LastSeen,
	}
	if online {
		pi.TotalSuccessfulPings = existing.TotalSuccessfulPings + 1
		pi.IsOnWatchlist = false
		pi.IsAdjusted = false
		pi.LastSeen = sql.NullTime{Time: now, Valid: true}
	}
	if pi.TotalSuccessfulPings > 0 {
		pi.AvgPingResponseTime = pi.CumulativeResponseTime / float64(pi.TotalSuccessfulPings)
	}
	_ = s.store.UpsertPingHistory(pi)
	_ = ctx
}

func (s *Service) updateWatchlist(ctx context.Context) {
	infos, err := s.store.GetAllPingInfos()
	if err != nil {
		return
	}
	cut := time.Now().UTC().Add(-s.cfg.WatchlistStaleFor)
	for _, info := range infos {
		if info.IsOnWatchlist || info.IsAdjusted || !info.LastSeen.Valid {
			continue
		}
		if info.LastSeen.Time.Before(cut) {
			_ = s.store.UpdatePingInfo(info.SupernodeID, true, false)
		}
	}
	_ = ctx
}

func (s *Service) generateChallenges(ctx context.Context, now time.Time) {
	active, online, _, err := s.networkSnapshot(ctx)
	if err != nil || len(active) == 0 {
		return
	}

	watch, err := s.auditWeightedWatchlist(ctx, active)
	if err != nil {
		logtrace.Warn(ctx, "self-healing weighted watchlist unavailable", logtrace.Fields{"error": err.Error()})
		return
	}
	if len(watch) < s.cfg.WatchlistThreshold {
		logtrace.Debug(ctx, "self-healing trigger skipped: watchlist below threshold", logtrace.Fields{"watchlist_count": len(watch), "threshold": s.cfg.WatchlistThreshold})
		return
	}

	windowID := now.Truncate(s.cfg.GenerationInterval).Unix()
	leader := electLeader(active, windowID, hashList(watch))
	if leader != s.identity {
		return
	}

	targets, err := s.listCascadeHealingTargets(ctx)
	if err != nil {
		logtrace.Warn(ctx, "self-healing action target listing failed", logtrace.Fields{"error": err.Error()})
		return
	}
	if len(targets) == 0 {
		return
	}

	watchSet := toSet(watch)
	eligibleRecipients := filterEligibleRecipients(online, watchSet, s.identity)
	if len(eligibleRecipients) == 0 {
		return
	}

	triggerID := fmt.Sprintf("window:%d", windowID)
	epochID := uint64(0)
	if s.lumera != nil && s.lumera.Audit() != nil {
		if ep, err := s.lumera.Audit().GetCurrentEpoch(ctx); err == nil && ep != nil {
			epochID = ep.EpochId
		}
	}

	selectedTargets := selectWindowTargets(targets, windowID, s.cfg.MaxChallenges)
	if len(selectedTargets) < len(targets) {
		logtrace.Info(ctx, "self-healing target set capped for window", logtrace.Fields{
			"window_id":          windowID,
			"total_targets":      len(targets),
			"selected_targets":   len(selectedTargets),
			"max_challenges":     s.cfg.MaxChallenges,
			"selection_strategy": "window_offset",
		})
	}
	if s.cfg.RequireDirectMissingEvidence {
		selectedTargets = s.filterTargetsByDirectEvidence(ctx, selectedTargets)
		if len(selectedTargets) == 0 {
			return
		}
	}

	events := make([]types.SelfHealingChallengeEvent, 0, len(selectedTargets))
	for _, target := range selectedTargets {
		holders, err := deterministic.SelectReplicaSet(active, target.FileKey, uint32(maxInt(1, s.cfg.ClosestNodes)))
		if err != nil || len(holders) == 0 {
			continue
		}
		if !allInSet(holders, watchSet) {
			continue
		}
		recipient := pickDeterministicNode(eligibleRecipients, target.FileKey)
		if recipient == "" {
			continue
		}
		obsPool := removeOne(eligibleRecipients, recipient)
		observers := pickTopNDeterministic(obsPool, target.FileKey+":obs", s.cfg.ObserverCount)
		challengeID := deriveWindowChallengeID(windowID, target.ActionID)

		payload := challengeEventPayload{
			EpochID:    epochID,
			WindowID:   windowID,
			ActionID:   target.ActionID,
			FileKey:    target.FileKey,
			Recipient:  recipient,
			Observers:  observers,
			WatchHash:  hashList(watch),
			ActiveHash: hashList(active),
		}
		bz, _ := json.Marshal(payload)
		events = append(events, types.SelfHealingChallengeEvent{
			TriggerID:   triggerID,
			TicketID:    target.ActionID,
			ChallengeID: challengeID,
			Data:        bz,
			SenderID:    s.identity,
			ExecMetric: types.SelfHealingExecutionMetric{
				TriggerID:   triggerID,
				ChallengeID: challengeID,
				MessageType: int(types.SelfHealingChallengeMessage),
				Data:        bz,
				SenderID:    s.identity,
			},
		})
	}

	if len(events) == 0 {
		return
	}
	if err := s.store.BatchInsertSelfHealingChallengeEvents(ctx, events); err != nil {
		logtrace.Warn(ctx, "self-healing generation insert failed", logtrace.Fields{"error": err.Error()})
		return
	}
	logtrace.Info(ctx, "self-healing generated challenges", logtrace.Fields{"window_id": windowID, "count": len(events)})
}

func (s *Service) processEvents(ctx context.Context) {
	owner := s.identity
	events, err := s.store.ClaimPendingSelfHealingChallengeEvents(ctx, owner, s.cfg.EventLeaseDuration, s.cfg.MaxEventsPerTick)
	if err != nil || len(events) == 0 {
		return
	}
	workers := s.cfg.EventWorkers
	if workers <= 1 || len(events) == 1 {
		for _, event := range events {
			s.handleClaimedEvent(ctx, owner, event)
		}
		return
	}
	if workers > len(events) {
		workers = len(events)
	}

	jobs := make(chan types.SelfHealingChallengeEvent, len(events))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range jobs {
				s.handleClaimedEvent(ctx, owner, event)
			}
		}()
	}
enqueue:
	for _, event := range events {
		select {
		case <-ctx.Done():
			break enqueue
		case jobs <- event:
		}
	}
	close(jobs)
	wg.Wait()
}

func (s *Service) handleClaimedEvent(ctx context.Context, owner string, event types.SelfHealingChallengeEvent) {
	if err := s.processEvent(ctx, event); err != nil {
		reason := err.Error()
		retryable := true
		var processErr *eventProcessError
		if errors.As(err, &processErr) {
			reason = processErr.Reason
			retryable = processErr.Retryable
		}

		attempted := event.AttemptCount
		maxed := attempted >= s.cfg.MaxEventAttempts
		if !retryable || maxed {
			if terr := s.store.MarkSelfHealingChallengeEventTerminal(event.ChallengeID, owner, reason); terr != nil {
				logtrace.Warn(ctx, "self-healing event terminal update failed", logtrace.Fields{"challenge_id": event.ChallengeID, "error": terr.Error()})
			}
			logtrace.Warn(ctx, "self-healing event terminal failure", logtrace.Fields{"challenge_id": event.ChallengeID, "reason": reason, "attempt_count": attempted})
			return
		}

		delay := s.retryDelayForAttempt(attempted)
		if rerr := s.store.MarkSelfHealingChallengeEventRetry(event.ChallengeID, owner, reason, delay); rerr != nil {
			logtrace.Warn(ctx, "self-healing event retry update failed", logtrace.Fields{"challenge_id": event.ChallengeID, "error": rerr.Error()})
		}
		logtrace.Warn(ctx, "self-healing event process failed", logtrace.Fields{"challenge_id": event.ChallengeID, "reason": reason, "attempt_count": attempted, "retry_after_ms": delay.Milliseconds()})
		return
	}
	if err := s.store.MarkSelfHealingChallengeEventCompleted(event.ChallengeID, owner); err != nil {
		logtrace.Warn(ctx, "self-healing event complete update failed", logtrace.Fields{"challenge_id": event.ChallengeID, "error": err.Error()})
	}
}

func (s *Service) processEvent(ctx context.Context, event types.SelfHealingChallengeEvent) error {
	var pl challengeEventPayload
	if err := json.Unmarshal(event.Data, &pl); err != nil {
		return &eventProcessError{Reason: "invalid_payload", Retryable: false, Err: err}
	}
	if strings.TrimSpace(pl.FileKey) == "" || strings.TrimSpace(pl.Recipient) == "" {
		return &eventProcessError{Reason: "invalid_payload_fields", Retryable: false}
	}
	if pl.WindowID > 0 {
		windowStart := time.Unix(pl.WindowID, 0).UTC()
		if time.Now().UTC().After(windowStart.Add(s.cfg.MaxWindowAge)) {
			s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": "stale_window"})
			return &eventProcessError{Reason: "stale_window", Retryable: false}
		}
	}
	recipientAddr, err := s.supernodeGRPCAddr(ctx, pl.Recipient)
	if err != nil {
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": "recipient_resolve_failed"})
		return &eventProcessError{Reason: "recipient_resolve_failed", Retryable: true, Err: err}
	}

	req := &supernode.RequestSelfHealingRequest{
		ChallengeId:  event.ChallengeID,
		EpochId:      pl.EpochID,
		FileKey:      pl.FileKey,
		ChallengerId: s.identity,
		RecipientId:  pl.Recipient,
		ObserverIds:  pl.Observers,
		ActionId:     pl.ActionID,
	}
	resp, err := s.requestSelfHealing(ctx, pl.Recipient, recipientAddr, req, requestTimeout)
	if err != nil || resp == nil || !resp.Accepted {
		reason := "request_failed"
		if resp != nil && strings.TrimSpace(resp.Error) != "" {
			reason = resp.Error
		}
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": reason})
		if err != nil {
			return &eventProcessError{Reason: "request_rpc_error", Retryable: true, Err: err}
		}
		lowerReason := strings.ToLower(strings.TrimSpace(reason))
		if strings.Contains(lowerReason, "stale") {
			return &eventProcessError{Reason: lowerReason, Retryable: false}
		}
		return &eventProcessError{Reason: lowerReason, Retryable: true}
	}
	s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingResponseMessage), map[string]any{"event": "response", "reconstruction_required": resp.ReconstructionRequired, "reconstructed_hash_hex": resp.ReconstructedHashHex})

	if !resp.ReconstructionRequired {
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "completed", "result": "not_required"})
		return nil
	}
	okCount := 0
	for _, ob := range pl.Observers {
		addr, err := s.supernodeGRPCAddr(ctx, ob)
		if err != nil {
			continue
		}
		vr, verr := s.verifySelfHealing(ctx, ob, addr, &supernode.VerifySelfHealingRequest{
			ChallengeId:          event.ChallengeID,
			EpochId:              pl.EpochID,
			FileKey:              pl.FileKey,
			RecipientId:          pl.Recipient,
			ReconstructedHashHex: resp.ReconstructedHashHex,
			ObserverId:           ob,
			ActionId:             pl.ActionID,
		}, verificationTimout)
		ok := verr == nil && vr != nil && vr.Ok
		if ok {
			okCount++
		}
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingVerificationMessage), map[string]any{"event": "observer_result", "observer_id": ob, "ok": ok})
	}
	if okCount < s.cfg.ObserverThreshold {
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": "observer_quorum_failed", "ok_count": okCount})
		return &eventProcessError{Reason: "observer_quorum_failed", Retryable: true}
	}
	commitReq := &supernode.CommitSelfHealingRequest{
		ChallengeId:  event.ChallengeID,
		EpochId:      pl.EpochID,
		FileKey:      pl.FileKey,
		ActionId:     pl.ActionID,
		ChallengerId: s.identity,
		RecipientId:  pl.Recipient,
	}
	commitResp, cerr := s.commitSelfHealing(ctx, pl.Recipient, recipientAddr, commitReq, commitTimeout)
	if cerr != nil || commitResp == nil || !commitResp.Stored {
		reason := "commit_failed"
		if commitResp != nil && strings.TrimSpace(commitResp.Error) != "" {
			reason = strings.ToLower(strings.TrimSpace(commitResp.Error))
		}
		if cerr != nil {
			s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": reason, "error": cerr.Error()})
			return &eventProcessError{Reason: "commit_rpc_error", Retryable: true, Err: cerr}
		}
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": reason})
		if strings.Contains(reason, "stale") {
			return &eventProcessError{Reason: reason, Retryable: false}
		}
		return &eventProcessError{Reason: reason, Retryable: true}
	}
	s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "completed", "result": "healed", "ok_count": okCount})
	return nil
}

func (s *Service) networkSnapshot(ctx context.Context) (active []string, online []string, addrMap map[string]string, err error) {
	resp, err := s.lumera.SuperNode().ListSuperNodes(ctx)
	if err != nil || resp == nil {
		return nil, nil, nil, fmt.Errorf("list supernodes: %w", err)
	}
	addrMap = map[string]string{}
	for _, sn := range resp.Supernodes {
		if sn == nil || strings.TrimSpace(sn.SupernodeAccount) == "" {
			continue
		}
		if !isActiveSN(sn) {
			continue
		}
		id := strings.TrimSpace(sn.SupernodeAccount)
		active = append(active, id)
		latest, _ := s.lumera.SuperNode().GetSupernodeWithLatestAddress(ctx, id)
		if latest != nil {
			addrMap[id] = latest.LatestAddress
		}
		pi, _ := s.store.GetPingInfoBySupernodeID(id)
		if pi != nil && pi.IsOnline {
			online = append(online, id)
		}
	}
	sort.Strings(active)
	sort.Strings(online)
	return active, online, addrMap, nil
}

func (s *Service) auditWeightedWatchlist(ctx context.Context, active []string) ([]string, error) {
	if s.lumera == nil || s.lumera.Audit() == nil {
		return nil, fmt.Errorf("audit module unavailable")
	}

	paramsResp, err := s.lumera.Audit().GetParams(ctx)
	if err != nil || paramsResp == nil {
		return nil, fmt.Errorf("get audit params: %w", err)
	}
	params := paramsResp.Params.WithDefaults()
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid audit params: %w", err)
	}

	epochResp, err := s.lumera.Audit().GetCurrentEpoch(ctx)
	if err != nil || epochResp == nil {
		return nil, fmt.Errorf("get current epoch: %w", err)
	}
	epochID := epochResp.EpochId

	requiredPorts := len(params.RequiredOpenPorts)
	if requiredPorts == 0 {
		return nil, nil
	}

	minReporters := int(params.PeerQuorumReports)
	if minReporters <= 0 {
		minReporters = 1
	}
	thresholdPercent := int(params.PeerPortPostponeThresholdPercent)
	if thresholdPercent <= 0 {
		thresholdPercent = 100
	}

	watch := make([]string, 0)
	for _, target := range active {
		if target == s.identity {
			continue
		}

		reportsResp, err := s.lumera.Audit().GetStorageChallengeReports(ctx, target, epochID)
		if err != nil || reportsResp == nil {
			continue
		}

		total, closed := countWeightedClosedVotes(reportsResp.Reports, requiredPorts)
		if total < minReporters {
			continue
		}
		if closed*100 >= thresholdPercent*total {
			watch = append(watch, target)
		}
	}

	sort.Strings(watch)
	logtrace.Debug(ctx, "self-healing weighted watchlist computed", logtrace.Fields{
		"epoch_id":        epochID,
		"watchlist_count": len(watch),
		"active_count":    len(active),
		"min_reporters":   minReporters,
		"threshold_pct":   thresholdPercent,
	})
	return watch, nil
}

func countWeightedClosedVotes(reports []audittypes.StorageChallengeReport, requiredPortsLen int) (total int, closed int) {
	if requiredPortsLen <= 0 {
		return 0, 0
	}
	reporterVotes := make(map[string]bool, len(reports))
	for _, report := range reports {
		reporter := strings.TrimSpace(report.ReporterSupernodeAccount)
		if reporter == "" {
			continue
		}
		if _, exists := reporterVotes[reporter]; exists {
			continue
		}
		known, isClosed := classifyReporterObservation(report.PortStates, requiredPortsLen)
		if !known {
			continue
		}
		reporterVotes[reporter] = isClosed
	}

	for _, isClosed := range reporterVotes {
		total++
		if isClosed {
			closed++
		}
	}
	return total, closed
}

func classifyReporterObservation(states []audittypes.PortState, requiredPortsLen int) (known bool, isClosed bool) {
	if len(states) != requiredPortsLen {
		return false, false
	}
	allOpen := true
	hasClosed := false
	for _, st := range states {
		switch st {
		case audittypes.PortState_PORT_STATE_OPEN:
			continue
		case audittypes.PortState_PORT_STATE_CLOSED:
			hasClosed = true
			allOpen = false
		default:
			// Unknown observations are intentionally excluded from weighting.
			allOpen = false
		}
	}
	if hasClosed {
		return true, true
	}
	if allOpen {
		return true, false
	}
	return false, false
}

type actionHealingTarget struct {
	ActionID      string
	FileKey       string
	ActionHashHex string
}

func (s *Service) listCascadeHealingTargets(ctx context.Context) ([]actionHealingTarget, error) {
	if cached, ok := s.cachedTargets(); ok {
		return cached, nil
	}

	s.targetsCacheMu.Lock()
	defer s.targetsCacheMu.Unlock()
	// Re-check under write lock in case another caller refreshed already.
	if cached, ok := s.cachedTargetsLocked(); ok {
		return cached, nil
	}

	if s.lumera == nil || s.lumera.Action() == nil {
		return nil, fmt.Errorf("action module unavailable")
	}

	states := []actiontypes.ActionState{
		actiontypes.ActionStateDone,
		actiontypes.ActionStateApproved,
	}

	targets := make([]actionHealingTarget, 0)
	seenByAction := make(map[string]struct{})
	for _, state := range states {
		var nextKey []byte
		for {
			resp, err := s.lumera.Action().ListActions(ctx, &actiontypes.QueryListActionsRequest{
				ActionType:  actiontypes.ActionTypeCascade,
				ActionState: state,
				Pagination: &query.PageRequest{
					Key:   nextKey,
					Limit: uint64(s.cfg.ActionPageLimit),
				},
			})
			if err != nil {
				return nil, fmt.Errorf("list cascade actions (state=%s): %w", state.String(), err)
			}
			if resp == nil {
				break
			}

			for _, act := range resp.Actions {
				if act == nil {
					continue
				}
				actionID := strings.TrimSpace(act.ActionID)
				if actionID == "" {
					continue
				}
				if _, seen := seenByAction[actionID]; seen {
					continue
				}
				metadata := act.Metadata
				if len(metadata) == 0 {
					continue
				}
				cascadeMeta, err := cascadekit.UnmarshalCascadeMetadata(metadata)
				if err != nil {
					continue
				}
				fileKey := pickActionAnchorKey(cascadeMeta.RqIdsIds)
				if fileKey == "" {
					continue
				}
				actionHashHex := ""
				if dataHash := strings.TrimSpace(cascadeMeta.DataHash); dataHash != "" {
					if raw, derr := base64.StdEncoding.DecodeString(dataHash); derr == nil && len(raw) > 0 {
						actionHashHex = strings.ToLower(hex.EncodeToString(raw))
					}
				}
				seenByAction[actionID] = struct{}{}
				targets = append(targets, actionHealingTarget{
					ActionID:      actionID,
					FileKey:       fileKey,
					ActionHashHex: actionHashHex,
				})
			}

			if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
				break
			}
			nextKey = append(nextKey[:0], resp.Pagination.NextKey...)
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ActionID == targets[j].ActionID {
			return targets[i].FileKey < targets[j].FileKey
		}
		return targets[i].ActionID < targets[j].ActionID
	})
	logtrace.Debug(ctx, "self-healing on-chain action targets listed", logtrace.Fields{
		"targets_count": len(targets),
	})
	s.targetsCache = append(s.targetsCache[:0], targets...)
	s.targetsCached = time.Now().UTC()
	return targets, nil
}

func (s *Service) cachedTargets() ([]actionHealingTarget, bool) {
	s.targetsCacheMu.RLock()
	defer s.targetsCacheMu.RUnlock()
	return s.cachedTargetsLocked()
}

func (s *Service) cachedTargetsLocked() ([]actionHealingTarget, bool) {
	if len(s.targetsCache) == 0 {
		return nil, false
	}
	if s.cfg.ActionTargetsTTL > 0 && time.Since(s.targetsCached) > s.cfg.ActionTargetsTTL {
		return nil, false
	}
	out := make([]actionHealingTarget, len(s.targetsCache))
	copy(out, s.targetsCache)
	return out, true
}

func (s *Service) hasDirectMissingEvidence(ctx context.Context, target actionHealingTarget) (bool, error) {
	if strings.TrimSpace(target.FileKey) == "" {
		return false, fmt.Errorf("empty file key")
	}
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.DirectProbeTimeout)
	defer cancel()

	data, err := s.p2p.Retrieve(probeCtx, target.FileKey)
	if err != nil {
		return true, nil
	}
	if len(data) == 0 {
		return true, nil
	}
	expected := strings.TrimSpace(strings.ToLower(target.ActionHashHex))
	if expected == "" {
		return false, nil
	}
	sum := blake3.Sum256(data)
	got := strings.ToLower(hex.EncodeToString(sum[:]))
	if !strings.EqualFold(got, expected) {
		return true, nil
	}
	return false, nil
}

func (s *Service) filterTargetsByDirectEvidence(ctx context.Context, targets []actionHealingTarget) []actionHealingTarget {
	if len(targets) == 0 {
		return nil
	}
	workers := s.cfg.DirectProbeWorkers
	if workers <= 1 || len(targets) == 1 {
		out := make([]actionHealingTarget, 0, len(targets))
		for _, target := range targets {
			missing, err := s.hasDirectMissingEvidence(ctx, target)
			if err != nil {
				logtrace.Warn(ctx, "self-healing direct evidence probe failed", logtrace.Fields{
					"action_id": target.ActionID,
					"file_key":  target.FileKey,
					"error":     err.Error(),
				})
				continue
			}
			if missing {
				out = append(out, target)
			}
		}
		return out
	}
	if workers > len(targets) {
		workers = len(targets)
	}

	type probeResult struct {
		target  actionHealingTarget
		missing bool
	}

	jobs := make(chan actionHealingTarget, len(targets))
	results := make(chan probeResult, len(targets))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				missing, err := s.hasDirectMissingEvidence(ctx, target)
				if err != nil {
					logtrace.Warn(ctx, "self-healing direct evidence probe failed", logtrace.Fields{
						"action_id": target.ActionID,
						"file_key":  target.FileKey,
						"error":     err.Error(),
					})
					continue
				}
				results <- probeResult{target: target, missing: missing}
			}
		}()
	}

	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]actionHealingTarget, 0, len(targets))
	for res := range results {
		if res.missing {
			out = append(out, res.target)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ActionID == out[j].ActionID {
			return out[i].FileKey < out[j].FileKey
		}
		return out[i].ActionID < out[j].ActionID
	})
	return out
}

func pickActionAnchorKey(keys []string) string {
	anchor := ""
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if anchor == "" || key < anchor {
			anchor = key
		}
	}
	return anchor
}

func selectWindowTargets(targets []actionHealingTarget, windowID int64, limit int) []actionHealingTarget {
	n := len(targets)
	if n == 0 || limit <= 0 || n <= limit {
		out := make([]actionHealingTarget, n)
		copy(out, targets)
		return out
	}

	start := int(windowID % int64(n))
	if start < 0 {
		start += n
	}
	out := make([]actionHealingTarget, 0, limit)
	for i := 0; i < limit; i++ {
		idx := (start + i) % n
		out = append(out, targets[idx])
	}
	return out
}

func isActiveSN(sn *sntypes.SuperNode) bool {
	var latest *sntypes.SuperNodeStateRecord
	for _, st := range sn.States {
		if st == nil {
			continue
		}
		if latest == nil || st.Height > latest.Height {
			latest = st
		}
	}
	if latest == nil {
		return false
	}
	return latest.State == sntypes.SuperNodeStateActive
}

func (s *Service) persistExecution(triggerID, challengeID string, msgType int, payload map[string]any) {
	bz, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.store.InsertSelfHealingExecutionMetrics(types.SelfHealingExecutionMetric{TriggerID: triggerID, ChallengeID: challengeID, MessageType: msgType, Data: bz, SenderID: s.identity})
}

func (s *Service) supernodeGRPCAddr(ctx context.Context, supernodeAccount string) (string, error) {
	info, err := s.lumera.SuperNode().GetSupernodeWithLatestAddress(ctx, supernodeAccount)
	if err != nil || info == nil {
		return "", fmt.Errorf("resolve supernode address: %w", err)
	}
	raw := strings.TrimSpace(info.LatestAddress)
	if raw == "" {
		return "", fmt.Errorf("no ip address for supernode %s", supernodeAccount)
	}
	host, port, ok := parseHostAndPort(raw, int(s.grpcPort))
	if !ok || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("invalid supernode address for %s: %q", supernodeAccount, raw)
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}

func (s *Service) retryDelayForAttempt(attempt int) time.Duration {
	if attempt <= 0 {
		return s.cfg.EventRetryBase
	}
	delay := s.cfg.EventRetryBase
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= s.cfg.EventRetryMax {
			return s.cfg.EventRetryMax
		}
	}
	if delay > s.cfg.EventRetryMax {
		return s.cfg.EventRetryMax
	}
	return delay
}

func (s *Service) requestSelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
	if s.requestSelfHealingFn != nil {
		return s.requestSelfHealingFn(ctx, remoteIdentity, address, req, timeout)
	}
	return s.callRequestSelfHealing(ctx, remoteIdentity, address, req, timeout)
}

func (s *Service) verifySelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySelfHealingRequest, timeout time.Duration) (*supernode.VerifySelfHealingResponse, error) {
	if s.verifySelfHealingFn != nil {
		return s.verifySelfHealingFn(ctx, remoteIdentity, address, req, timeout)
	}
	return s.callVerifySelfHealing(ctx, remoteIdentity, address, req, timeout)
}

func (s *Service) commitSelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.CommitSelfHealingRequest, timeout time.Duration) (*supernode.CommitSelfHealingResponse, error) {
	if s.commitSelfHealingFn != nil {
		return s.commitSelfHealingFn(ctx, remoteIdentity, address, req, timeout)
	}
	return s.callCommitSelfHealing(ctx, remoteIdentity, address, req, timeout)
}

func (s *Service) callRequestSelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.RequestSelfHealingRequest, timeout time.Duration) (*supernode.RequestSelfHealingResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := s.grpcClient.Connect(cctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), s.grpcOpts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := supernode.NewSelfHealingServiceClient(conn)
	return client.RequestSelfHealing(cctx, req)
}

func (s *Service) callVerifySelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySelfHealingRequest, timeout time.Duration) (*supernode.VerifySelfHealingResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := s.grpcClient.Connect(cctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), s.grpcOpts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := supernode.NewSelfHealingServiceClient(conn)
	return client.VerifySelfHealing(cctx, req)
}

func (s *Service) callCommitSelfHealing(ctx context.Context, remoteIdentity string, address string, req *supernode.CommitSelfHealingRequest, timeout time.Duration) (*supernode.CommitSelfHealingResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := s.grpcClient.Connect(cctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), s.grpcOpts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	client := supernode.NewSelfHealingServiceClient(conn)
	return client.CommitSelfHealing(cctx, req)
}

func parseHostAndPort(address string, defaultPort int) (host string, port int, ok bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false
	}
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
	}
	if h, p, err := net.SplitHostPort(address); err == nil {
		h = strings.TrimSpace(h)
		if h == "" {
			return "", 0, false
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			return h, n, true
		}
		return h, defaultPort, true
	}
	host = strings.TrimSpace(address)
	if host == "" {
		return "", 0, false
	}
	if strings.ContainsAny(host, " \t\r\n/\\?#@[]") {
		return "", 0, false
	}
	return host, defaultPort, true
}

func deriveWindowChallengeID(windowID int64, fileKey string) string {
	msg := []byte(fmt.Sprintf("sh:v1:%d:%s", windowID, fileKey))
	sum := blake3.Sum256(msg)
	return hex.EncodeToString(sum[:])
}

func electLeader(nodes []string, windowID int64, salt string) string {
	best := ""
	var bestScore uint64
	for i, n := range nodes {
		h := fnv.New64a()
		_, _ = h.Write([]byte(fmt.Sprintf("%d:%s:%s", windowID, salt, n)))
		s := h.Sum64()
		if i == 0 || s < bestScore {
			bestScore = s
			best = n
		}
	}
	return best
}

func hashList(items []string) string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	msg := strings.Join(cp, ",")
	sum := blake3.Sum256([]byte(msg))
	return hex.EncodeToString(sum[:])
}

func toSet(items []string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

func allInSet(items []string, set map[string]struct{}) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if _, ok := set[it]; !ok {
			return false
		}
	}
	return true
}

func filterEligibleRecipients(online []string, watch map[string]struct{}, self string) []string {
	out := make([]string, 0)
	for _, n := range online {
		if n == self {
			continue
		}
		if _, bad := watch[n]; bad {
			continue
		}
		out = append(out, n)
	}
	return out
}

func pickDeterministicNode(nodes []string, key string) string {
	if len(nodes) == 0 {
		return ""
	}
	sel, err := deterministic.SelectReplicaSet(nodes, key, 1)
	if err != nil || len(sel) == 0 {
		return ""
	}
	return sel[0]
}

func pickTopNDeterministic(nodes []string, key string, n int) []string {
	if len(nodes) == 0 || n <= 0 {
		return nil
	}
	sel, err := deterministic.SelectReplicaSet(nodes, key, uint32(n))
	if err != nil {
		return nil
	}
	return sel
}

func removeOne(items []string, target string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it == target {
			continue
		}
		out = append(out, it)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
