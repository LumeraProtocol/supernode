package self_healing

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
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

	requestTimeout     = 20 * time.Second
	verificationTimout = 20 * time.Second
)

type Config struct {
	Enabled      bool
	PollInterval time.Duration
	KeyName      string

	PingInterval       time.Duration
	WatchlistInterval  time.Duration
	GenerationInterval time.Duration
	ProcessInterval    time.Duration

	WatchlistThreshold int
	WatchlistStaleFor  time.Duration
	WatchlistFreshFor  time.Duration
	ClosestNodes       int
	ObserverCount      int
	ObserverThreshold  int
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

	lastPingAt       time.Time
	lastWatchlistAt  time.Time
	lastGenerateAt   time.Time
	lastProcessEvtAt time.Time
}

type challengeEventPayload struct {
	WindowID   int64    `json:"window_id"`
	FileKey    string   `json:"file_key"`
	Recipient  string   `json:"recipient"`
	Observers  []string `json:"observers"`
	WatchHash  string   `json:"watch_hash"`
	ActiveHash string   `json:"active_hash"`
}

func NewService(identity string, grpcPort uint16, lumeraClient lumera.Client, p2pClient p2p.Client, kr keyring.Keyring, store queries.LocalStoreInterface, cfg Config) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.SuperNode() == nil {
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
	watchInfos, err := s.store.GetWatchlistPingInfo()
	if err != nil {
		return
	}
	freshCut := now.Add(-s.cfg.WatchlistFreshFor)
	watch := make([]string, 0)
	for _, p := range watchInfos {
		if p.LastSeen.Valid && p.LastSeen.Time.After(freshCut) {
			watch = append(watch, p.SupernodeID)
		}
	}
	sort.Strings(watch)
	if len(watch) < s.cfg.WatchlistThreshold {
		return
	}

	active, online, _, err := s.networkSnapshot(ctx)
	if err != nil || len(active) == 0 {
		return
	}
	windowID := now.Truncate(s.cfg.GenerationInterval).Unix()
	leader := electLeader(active, windowID, hashList(watch))
	if leader != s.identity {
		return
	}

	to := now
	from := to.Add(-24 * time.Hour)
	keys, err := s.p2p.GetLocalKeys(ctx, &from, to)
	if err != nil || len(keys) == 0 {
		return
	}
	sort.Strings(keys)

	watchSet := toSet(watch)
	eligibleRecipients := filterEligibleRecipients(online, watchSet, s.identity)
	if len(eligibleRecipients) == 0 {
		return
	}

	triggerID := fmt.Sprintf("window:%d", windowID)
	events := make([]types.SelfHealingChallengeEvent, 0)
	for _, k := range keys {
		holders, err := deterministic.SelectReplicaSet(active, k, uint32(maxInt(1, s.cfg.ClosestNodes)))
		if err != nil || len(holders) == 0 {
			continue
		}
		if !allInSet(holders, watchSet) {
			continue
		}
		recipient := pickDeterministicNode(eligibleRecipients, k)
		if recipient == "" {
			continue
		}
		obsPool := removeOne(eligibleRecipients, recipient)
		observers := pickTopNDeterministic(obsPool, k+":obs", s.cfg.ObserverCount)
		challengeID := deriveWindowChallengeID(windowID, k, recipient)

		payload := challengeEventPayload{WindowID: windowID, FileKey: k, Recipient: recipient, Observers: observers, WatchHash: hashList(watch), ActiveHash: hashList(active)}
		bz, _ := json.Marshal(payload)
		events = append(events, types.SelfHealingChallengeEvent{
			TriggerID:   triggerID,
			TicketID:    k,
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
	events, err := s.store.GetSelfHealingChallengeEvents()
	if err != nil || len(events) == 0 {
		return
	}
	for _, event := range events {
		if err := s.processEvent(ctx, event); err != nil {
			logtrace.Warn(ctx, "self-healing event process failed", logtrace.Fields{"challenge_id": event.ChallengeID, "error": err.Error()})
			continue
		}
		_ = s.store.UpdateSHChallengeEventProcessed(event.ChallengeID, true)
	}
}

func (s *Service) processEvent(ctx context.Context, event types.SelfHealingChallengeEvent) error {
	var pl challengeEventPayload
	if err := json.Unmarshal(event.Data, &pl); err != nil {
		return err
	}
	recipientAddr, err := s.supernodeGRPCAddr(ctx, pl.Recipient)
	if err != nil {
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": "recipient_resolve_failed"})
		return err
	}

	req := &supernode.RequestSelfHealingRequest{ChallengeId: event.ChallengeID, EpochId: 0, FileKey: pl.FileKey, ChallengerId: s.identity, RecipientId: pl.Recipient, ObserverIds: pl.Observers}
	resp, err := s.callRequestSelfHealing(ctx, pl.Recipient, recipientAddr, req, requestTimeout)
	if err != nil || resp == nil || !resp.Accepted {
		reason := "request_failed"
		if resp != nil && strings.TrimSpace(resp.Error) != "" {
			reason = resp.Error
		}
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": reason})
		if err != nil {
			return err
		}
		return fmt.Errorf("%s", reason)
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
		vr, verr := s.callVerifySelfHealing(ctx, ob, addr, &supernode.VerifySelfHealingRequest{ChallengeId: event.ChallengeID, EpochId: 0, FileKey: pl.FileKey, RecipientId: pl.Recipient, ReconstructedHashHex: resp.ReconstructedHashHex, ObserverId: ob}, verificationTimout)
		ok := verr == nil && vr != nil && vr.Ok
		if ok {
			okCount++
		}
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingVerificationMessage), map[string]any{"event": "observer_result", "observer_id": ob, "ok": ok})
	}
	if okCount < s.cfg.ObserverThreshold {
		s.persistExecution(event.TriggerID, event.ChallengeID, int(types.SelfHealingCompletionMessage), map[string]any{"event": "failed", "reason": "observer_quorum_failed", "ok_count": okCount})
		return fmt.Errorf("observer quorum failed")
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

func deriveWindowChallengeID(windowID int64, fileKey, recipient string) string {
	msg := []byte(fmt.Sprintf("sh:v1:%d:%s:%s", windowID, fileKey, recipient))
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
