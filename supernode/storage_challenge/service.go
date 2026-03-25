package storage_challenge

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"lukechampine.com/blake3"
)

// Storage challenge (SC) execution knobs are intentionally owned by the supernode binary,
// not by on-chain params. The chain only needs:
// - sc_enabled: feature gate for evidence acceptance/validation
// - sc_challengers_per_epoch: deterministic challenger selection size
// - epoch cadence: epoch_zero_height + epoch_length_blocks
const (
	// scStartJitterMs is a deterministic delay applied before running the epoch, to spread load.
	scStartJitterMs = uint64(60_000) // 60s

	// scFilesPerChallenger is how many local file keys each challenger attempts per epoch.
	scFilesPerChallenger = uint32(2)

	// scReplicaCount is the size of the replica set considered for recipient/observer selection.
	scReplicaCount = uint32(5)

	// scObserverThreshold is the quorum of observer affirmations required for a successful proof.
	scObserverThreshold = uint32(2)

	// scMinSliceBytes/scMaxSliceBytes bound the requested proof slice range.
	scMinSliceBytes = uint64(1024)
	scMaxSliceBytes = uint64(65_536)

	// scResponseTimeout/scAffirmationTimeout are the gRPC timeouts for recipient proof and observer verification.
	scResponseTimeout       = 30 * time.Second
	scAffirmationTimeout    = 30 * time.Second
	scEvidenceSubmitTimeout = 20 * time.Second

	// scCandidateKeysLookbackEpochs is how many epochs back we look for candidate local keys.
	scCandidateKeysLookbackEpochs = uint32(1)
)

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
}

type Config struct {
	Enabled        bool
	PollInterval   time.Duration
	SubmitEvidence bool
	KeyName        string
}

func NewService(identity string, grpcPort uint16, lumeraClient lumera.Client, p2pClient p2p.Client, kr keyring.Keyring, store queries.LocalStoreInterface, cfg Config) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}
	if lumeraClient == nil || lumeraClient.Audit() == nil || lumeraClient.AuditMsg() == nil || lumeraClient.Node() == nil || lumeraClient.SuperNode() == nil {
		return nil, fmt.Errorf("lumera client is missing required modules")
	}
	if p2pClient == nil {
		return nil, fmt.Errorf("p2p client is nil")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring is nil")
	}
	if strings.TrimSpace(cfg.KeyName) == "" {
		return nil, fmt.Errorf("key name is empty")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}

	// Ensure keyring key address matches the configured identity (supernode_account).
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

	return &Service{
		cfg:      cfg,
		identity: identity,
		grpcPort: grpcPort,
		lumera:   lumeraClient,
		p2p:      p2pClient,
		kr:       kr,
		store:    store,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	if !s.cfg.Enabled {
		<-ctx.Done()
		return nil
	}

	if err := s.initClients(); err != nil {
		return err
	}

	// Effective knobs (production defaults). Jitter is bounded by the epoch length
	// to avoid sleeping past the epoch window on short epochs.
	lookbackEpochs := scCandidateKeysLookbackEpochs
	respTimeout := scResponseTimeout
	affirmTimeout := scAffirmationTimeout
	logtrace.Debug(ctx, "storage challenge runtime knobs", logtrace.Fields{
		"start_jitter_ms":         scStartJitterMs,
		"response_timeout_ms":     respTimeout.Milliseconds(),
		"affirmation_timeout_ms":  affirmTimeout.Milliseconds(),
		"submit_evidence_config":  s.cfg.SubmitEvidence,
		"poll_interval_ms":        s.cfg.PollInterval.Milliseconds(),
		"sc_files_per_challenger": scFilesPerChallenger,
		"sc_replica_count":        scReplicaCount,
		"sc_observer_threshold":   scObserverThreshold,
		"sc_keys_lookback_epochs": lookbackEpochs,
	})

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	var lastRunEpoch uint64
	var lastRunOK bool
	var loggedAlreadyRanEpoch uint64
	var loggedNotSelectedEpoch uint64
	var loggedDisabledEpoch uint64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			height, ok := s.latestHeight(ctx)
			if !ok {
				continue
			}

			params, ok := s.auditParams(ctx)
			if !ok {
				continue
			}

			epochID, ok := deterministic.EpochID(height, params.EpochZeroHeight, params.EpochLengthBlocks)
			if !ok {
				continue
			}
			if !params.ScEnabled {
				if loggedDisabledEpoch != epochID {
					logtrace.Debug(ctx, "storage challenge disabled by on-chain params", logtrace.Fields{"epoch_id": epochID})
					loggedDisabledEpoch = epochID
				}
				lastRunEpoch = epochID
				lastRunOK = true
				continue
			}
			if lastRunOK && lastRunEpoch == epochID {
				if loggedAlreadyRanEpoch != epochID {
					logtrace.Debug(ctx, "storage challenge already ran this epoch; skipping", logtrace.Fields{"epoch_id": epochID})
					loggedAlreadyRanEpoch = epochID
				}
				continue
			}

			anchorResp, err := s.lumera.Audit().GetEpochAnchor(ctx, epochID)
			if err != nil || anchorResp == nil || anchorResp.Anchor.EpochId != epochID {
				// Anchor may not be committed yet at epoch boundary; retry on next tick.
				continue
			}
			anchor := anchorResp.Anchor

			challengers := deterministic.SelectChallengers(anchor.ActiveSupernodeAccounts, anchor.Seed, epochID, params.ScChallengersPerEpoch)
			if !containsString(challengers, s.identity) {
				if loggedNotSelectedEpoch != epochID {
					logtrace.Debug(ctx, "storage challenge: not selected challenger; skipping", logtrace.Fields{
						"epoch_id": epochID,
						"identity": s.identity,
						"selected": len(challengers),
						"sc_param": params.ScChallengersPerEpoch,
					})
					loggedNotSelectedEpoch = epochID
				}
				lastRunEpoch = epochID
				lastRunOK = true
				continue
			}

			// Bound jitter by a conservative estimate of epoch duration (assume ~1s blocks).
			// This is intentionally simple and is primarily to avoid sleeping past the epoch window.
			jitterMaxMs := scStartJitterMs
			epochBudgetMs := uint64(params.EpochLengthBlocks) * 1000
			if epochBudgetMs > 0 && epochBudgetMs/2 < jitterMaxMs {
				jitterMaxMs = epochBudgetMs / 2
			}

			jitterMs := deterministic.DeterministicJitterMs(anchor.Seed, epochID, s.identity, jitterMaxMs)
			if jitterMs > 0 {
				logtrace.Debug(ctx, "storage challenge jitter sleep", logtrace.Fields{
					"epoch_id":      epochID,
					"jitter_ms":     jitterMs,
					"jitter_max_ms": jitterMaxMs,
					"challenger_id": s.identity,
				})
				timer := time.NewTimer(time.Duration(jitterMs) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}

			if err := s.runEpoch(ctx, anchor, params, lookbackEpochs, respTimeout, affirmTimeout); err != nil {
				logtrace.Warn(ctx, "storage challenge epoch run error", logtrace.Fields{
					"epoch_id": epochID,
					"error":    err.Error(),
				})
				lastRunEpoch = epochID
				lastRunOK = false
				continue
			}

			lastRunEpoch = epochID
			lastRunOK = true
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

func (s *Service) latestHeight(ctx context.Context) (int64, bool) {
	resp, err := s.lumera.Node().GetLatestBlock(ctx)
	if err != nil || resp == nil {
		return 0, false
	}
	if sdkBlk := resp.GetSdkBlock(); sdkBlk != nil {
		return sdkBlk.Header.Height, true
	}
	if blk := resp.GetBlock(); blk != nil {
		return blk.Header.Height, true
	}
	return 0, false
}

func (s *Service) auditParams(ctx context.Context) (audittypes.Params, bool) {
	resp, err := s.lumera.Audit().GetParams(ctx)
	if err != nil || resp == nil {
		return audittypes.Params{}, false
	}
	p := resp.Params.WithDefaults()
	if err := p.Validate(); err != nil {
		return audittypes.Params{}, false
	}
	return p, true
}

func (s *Service) runEpoch(ctx context.Context, anchor audittypes.EpochAnchor, params audittypes.Params, lookbackEpochs uint32, respTimeout time.Duration, affirmTimeout time.Duration) error {
	epochID := anchor.EpochId

	lookback := s.candidateKeysLookbackDuration(ctx, params, lookbackEpochs)
	to := time.Now().UTC()
	from := to.Add(-lookback)

	keys, err := s.p2p.GetLocalKeys(ctx, &from, to)
	if err != nil {
		return fmt.Errorf("get local keys: %w", err)
	}
	if len(keys) == 0 {
		logtrace.Debug(ctx, "storage challenge: no local keys to challenge", logtrace.Fields{"epoch_id": epochID})
		return nil
	}
	sort.Strings(keys)

	fileKeys := deterministic.SelectFileKeys(keys, anchor.Seed, epochID, s.identity, scFilesPerChallenger)
	if len(fileKeys) == 0 {
		return nil
	}
	logtrace.Debug(ctx, "storage challenge selected file keys", logtrace.Fields{
		"epoch_id":      epochID,
		"challenger_id": s.identity,
		"keys_total":    len(keys),
		"file_keys":     strings.Join(fileKeys, ","),
	})

	for _, fileKey := range fileKeys {
		if err := s.runChallengeForFile(ctx, anchor, params, fileKey, respTimeout, affirmTimeout); err != nil {
			logtrace.Warn(ctx, "storage challenge file run error", logtrace.Fields{
				"epoch_id": epochID,
				"file_key": fileKey,
				"error":    err.Error(),
			})
		}
	}

	return nil
}

func (s *Service) runChallengeForFile(ctx context.Context, anchor audittypes.EpochAnchor, params audittypes.Params, fileKey string, respTimeout time.Duration, affirmTimeout time.Duration) error {
	epochID := anchor.EpochId

	replicas, err := deterministic.SelectReplicaSet(anchor.ActiveSupernodeAccounts, fileKey, scReplicaCount)
	if err != nil {
		return err
	}

	recipient, observers := pickRecipientAndObservers(replicas, s.identity, int(scObserverThreshold))
	if recipient == "" {
		return nil
	}
	logtrace.Debug(ctx, "storage challenge selected recipient/observers", logtrace.Fields{
		"epoch_id":      epochID,
		"file_key":      fileKey,
		"challenger_id": s.identity,
		"recipient_id":  recipient,
		"observers":     strings.Join(observers, ","),
	})

	recipientAddr, err := s.supernodeGRPCAddr(ctx, recipient)
	if err != nil {
		return err
	}
	type observerPeer struct {
		id   string
		addr string
	}
	observerPeers := make([]observerPeer, 0, len(observers))
	for _, ob := range observers {
		addr, err := s.supernodeGRPCAddr(ctx, ob)
		if err != nil {
			continue
		}
		observerPeers = append(observerPeers, observerPeer{id: ob, addr: addr})
	}

	requestedStart := uint64(0)
	requestedEnd := scMinSliceBytes
	if requestedEnd == 0 {
		requestedEnd = 1024
	}
	if scMaxSliceBytes > 0 && requestedEnd > scMaxSliceBytes {
		requestedEnd = scMaxSliceBytes
	}

	challengeID := deriveChallengeID(anchor.Seed, epochID, fileKey, s.identity, recipient)

	req := &supernode.GetSliceProofRequest{
		ChallengeId:    challengeID,
		EpochId:        epochID,
		Seed:           anchor.Seed,
		FileKey:        fileKey,
		RequestedStart: requestedStart,
		RequestedEnd:   requestedEnd,
		ChallengerId:   s.identity,
		RecipientId:    recipient,
		ObserverIds:    append([]string(nil), observers...),
	}

	resp, err := s.callGetSliceProof(ctx, recipient, recipientAddr, req, respTimeout)
	if err != nil || resp == nil || !resp.Ok {
		failure := "RECIPIENT_ERROR"
		if err != nil {
			failure = "RECIPIENT_UNREACHABLE"
		}
		return s.maybeSubmitEvidence(ctx, params, epochID, challengeID, fileKey, recipient, failure, transcriptHash(challengeID, req, resp, nil, err))
	}

	sum := blake3.Sum256(resp.Slice)
	if got := hex.EncodeToString(sum[:]); got != strings.ToLower(resp.ProofHashHex) {
		return s.maybeSubmitEvidence(ctx, params, epochID, challengeID, fileKey, recipient, "INVALID_PROOF", transcriptHash(challengeID, req, resp, nil, fmt.Errorf("proof mismatch")))
	}

	okCount := 0
	required := int(scObserverThreshold)
	if required <= 0 {
		required = 0
	}

	observerResults := make([]*supernode.VerifySliceProofResponse, 0, len(observerPeers))
	if required > 0 && len(observerPeers) > 0 {
		verifyReq := &supernode.VerifySliceProofRequest{
			ChallengeId:  challengeID,
			EpochId:      epochID,
			FileKey:      fileKey,
			Start:        resp.Start,
			End:          resp.End,
			Slice:        resp.Slice,
			ProofHashHex: resp.ProofHashHex,
			ChallengerId: s.identity,
			RecipientId:  recipient,
		}

		timeout := scAffirmationTimeout
		if affirmTimeout > 0 {
			timeout = affirmTimeout
		}

		for _, peer := range observerPeers {
			vr, verr := s.callVerifySliceProof(ctx, peer.id, peer.addr, verifyReq, timeout)
			if verr == nil && vr != nil && vr.Ok {
				okCount++
			}
			observerResults = append(observerResults, vr)
		}
	}

	if required > 0 && okCount < required {
		return s.maybeSubmitEvidence(ctx, params, epochID, challengeID, fileKey, recipient, "OBSERVER_QUORUM_FAILED", transcriptHash(challengeID, req, resp, observerResults, fmt.Errorf("observer quorum failed")))
	}

	logtrace.Info(ctx, "storage challenge ok", logtrace.Fields{
		"epoch_id":      epochID,
		"challenge_id":  challengeID,
		"file_key":      fileKey,
		"recipient_id":  recipient,
		"observers_ok":  okCount,
		"observers_req": required,
	})

	return nil
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

	// The chain stores the supernode's reachable endpoint. Historically this has often been
	// registered as "host:port" (e.g. "<public-ip>:4444"). Storage challenge must tolerate
	// both forms:
	// - "host" -> use our configured default gRPC port
	// - "host:port" -> use the stored port as the dial target
	host, port, ok := parseHostAndPort(raw, int(s.grpcPort))
	if !ok || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("invalid supernode address for %s: %q", supernodeAccount, raw)
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}

// parseHostAndPort parses a "host" or "host:port" string and returns a host and port.
// If a port is not present, defaultPort is returned. If a port is present but invalid,
func parseHostAndPort(address string, defaultPort int) (host string, port int, ok bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false
	}

	// If it looks like a URL, parse and use the host[:port] portion.
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

	// No port present. Treat it as a raw host if it is plausibly valid; otherwise fail.
	host = strings.TrimSpace(address)
	if host == "" {
		return "", 0, false
	}

	// Accept bracketed IPv6 literal without a port (e.g. "[2001:db8::1]") by stripping brackets.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && strings.Count(host, "]") == 1 {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		host = strings.TrimSpace(host)
		if host == "" {
			return "", 0, false
		}
	}

	// Reject obviously malformed inputs (paths, fragments, userinfo, whitespace, or stray brackets).
	if strings.ContainsAny(host, " \t\r\n/\\?#@[]") {
		return "", 0, false
	}

	// If it contains ':' it must be a valid IPv6 literal (optionally with a zone, e.g. "fe80::1%eth0").
	if strings.Contains(host, ":") {
		ipPart := host
		if i := strings.IndexByte(ipPart, '%'); i >= 0 {
			ipPart = ipPart[:i]
		}
		if net.ParseIP(ipPart) == nil {
			return "", 0, false
		}
	}

	return host, defaultPort, true
}

func (s *Service) callGetSliceProof(ctx context.Context, remoteIdentity string, address string, req *supernode.GetSliceProofRequest, timeout time.Duration) (*supernode.GetSliceProofResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// secure gRPC requires the peer identity in the dial target
	// (format: "<remoteIdentity>@<host:port>") so the handshake can authenticate the peer.
	conn, err := s.grpcClient.Connect(cctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), s.grpcOpts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := supernode.NewStorageChallengeServiceClient(conn)
	return client.GetSliceProof(cctx, req)
}

func (s *Service) callVerifySliceProof(ctx context.Context, remoteIdentity string, address string, req *supernode.VerifySliceProofRequest, timeout time.Duration) (*supernode.VerifySliceProofResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Production behavior: secure gRPC requires "<remoteIdentity>@<host:port>" (see callGetSliceProof).
	conn, err := s.grpcClient.Connect(cctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), s.grpcOpts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := supernode.NewStorageChallengeServiceClient(conn)
	return client.VerifySliceProof(cctx, req)
}

func (s *Service) maybeSubmitEvidence(ctx context.Context, params audittypes.Params, epochID uint64, challengeID, fileKey, recipient, failureType, transcriptHashHex string) error {
	if !s.cfg.SubmitEvidence || !params.ScEnabled {
		logtrace.Debug(ctx, "storage challenge: evidence submission skipped", logtrace.Fields{
			"epoch_id":               epochID,
			"challenge_id":           challengeID,
			"recipient_id":           recipient,
			"failure_type":           failureType,
			"submit_evidence_config": s.cfg.SubmitEvidence,
			"sc_enabled_param":       params.ScEnabled,
		})
		return nil
	}

	meta := audittypes.StorageChallengeFailureEvidenceMetadata{
		EpochId:                    epochID,
		ChallengerSupernodeAccount: s.identity,
		ChallengedSupernodeAccount: recipient,
		ChallengeId:                challengeID,
		FileKey:                    fileKey,
		FailureType:                failureType,
		TranscriptHash:             transcriptHashHex,
	}
	bz, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	submitCtx, cancel := context.WithTimeout(ctx, scEvidenceSubmitTimeout)
	defer cancel()
	_ = submitCtx
	_ = bz

	// TEMPORARY INCIDENT MITIGATION: chain submission intentionally disabled.
	// _, err = s.lumera.AuditMsg().SubmitEvidence(submitCtx, recipient, audittypes.EvidenceType_EVIDENCE_TYPE_STORAGE_CHALLENGE_FAILURE, "", string(bz))
	if err != nil {
		return err
	}
	logtrace.Warn(ctx, "storage challenge failure evidence submission temporarily disabled", logtrace.Fields{
		"epoch_id":     epochID,
		"challenge_id": challengeID,
		"recipient_id": recipient,
		"failure_type": failureType,
	})
	return nil
}

func deriveChallengeID(seed []byte, epochID uint64, fileKey, challenger, recipient string) string {
	msg := []byte("sc:challenge:" + hex.EncodeToString(seed) + ":" + strconv.FormatUint(epochID, 10) + ":" + fileKey + ":" + challenger + ":" + recipient)
	sum := blake3.Sum256(msg)
	return hex.EncodeToString(sum[:])
}

func transcriptHash(challengeID string, req *supernode.GetSliceProofRequest, resp *supernode.GetSliceProofResponse, obs []*supernode.VerifySliceProofResponse, err error) string {
	type t struct {
		ChallengeID string                                `json:"challenge_id"`
		Request     *supernode.GetSliceProofRequest       `json:"request,omitempty"`
		Response    *supernode.GetSliceProofResponse      `json:"response,omitempty"`
		Observers   []*supernode.VerifySliceProofResponse `json:"observers,omitempty"`
		Error       string                                `json:"error,omitempty"`
	}
	out := t{ChallengeID: challengeID, Request: req, Response: resp, Observers: obs}
	if err != nil {
		out.Error = err.Error()
	}
	bz, _ := json.Marshal(out)
	sum := blake3.Sum256(bz)
	return hex.EncodeToString(sum[:])
}

func pickRecipientAndObservers(replicas []string, self string, observerCount int) (string, []string) {
	out := make([]string, 0, maxInt(0, observerCount))
	recipient := ""
	for _, r := range replicas {
		if r == self {
			continue
		}
		recipient = r
		break
	}
	if recipient == "" {
		return "", nil
	}
	for _, r := range replicas {
		if r == self || r == recipient {
			continue
		}
		out = append(out, r)
		if observerCount > 0 && len(out) >= observerCount {
			break
		}
	}
	sort.Strings(out)
	return recipient, out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (s *Service) candidateKeysLookbackDuration(ctx context.Context, params audittypes.Params, epochs uint32) time.Duration {
	if epochs == 0 {
		epochs = 1
	}

	epochDuration, ok := s.estimateEpochDuration(ctx, params)
	if !ok || epochDuration <= 0 {
		// Fallback to legacy "1 epoch == 24h" assumption if we can't estimate block time.
		return time.Duration(epochs) * 24 * time.Hour
	}
	return time.Duration(epochs) * epochDuration
}

func (s *Service) estimateEpochDuration(ctx context.Context, params audittypes.Params) (time.Duration, bool) {
	if params.EpochLengthBlocks == 0 {
		return 0, false
	}

	latest, err := s.lumera.Node().GetLatestBlock(ctx)
	if err != nil || latest == nil {
		return 0, false
	}
	var latestHeight int64
	var latestTime time.Time
	if sdkBlk := latest.GetSdkBlock(); sdkBlk != nil {
		latestHeight = sdkBlk.Header.Height
		latestTime = sdkBlk.Header.Time
	} else if blk := latest.GetBlock(); blk != nil {
		latestHeight = blk.Header.Height
		latestTime = blk.Header.Time
	} else {
		return 0, false
	}
	if latestHeight <= 1 {
		return 0, false
	}

	// Sample over the last N blocks to smooth variance.
	const sampleBlocks int64 = 100
	n := sampleBlocks
	if latestHeight <= sampleBlocks {
		n = latestHeight - 1
	}
	olderHeight := latestHeight - n

	older, err := s.lumera.Node().GetBlockByHeight(ctx, olderHeight)
	if err != nil || older == nil {
		return 0, false
	}
	var olderTime time.Time
	if sdkBlk := older.GetSdkBlock(); sdkBlk != nil {
		olderTime = sdkBlk.Header.Time
	} else if blk := older.GetBlock(); blk != nil {
		olderTime = blk.Header.Time
	} else {
		return 0, false
	}

	dt := latestTime.Sub(olderTime)
	if dt <= 0 {
		return 0, false
	}
	avgBlockTime := dt / time.Duration(n)
	if avgBlockTime <= 0 {
		return 0, false
	}

	// Guard against wildly wrong clocks.
	if avgBlockTime > 2*time.Minute {
		return 0, false
	}

	// epoch_duration ~= epoch_length_blocks * avg_block_time
	epochBlocks := params.EpochLengthBlocks
	if epochBlocks > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return time.Duration(epochBlocks) * avgBlockTime, true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
