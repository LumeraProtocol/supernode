package self_healing

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	query "github.com/cosmos/cosmos-sdk/types/query"
	"golang.org/x/sync/singleflight"
	"lukechampine.com/blake3"
)

const epochSkewTolerance = uint64(2)
const defaultActionPageLimit = 200
const defaultActionIndexTTL = 5 * time.Minute
const defaultPerPeerRateLimitPerMin = 60
const defaultPerPeerBurst = 20
const defaultPerPeerMaxInFlight = 2
const defaultGlobalMaxInFlight = 32
const defaultRecoveryTimeout = 30 * time.Second
const defaultBreakerFailThreshold = 5
const defaultBreakerCooldown = 2 * time.Minute
const defaultBreakerHalfOpenPermits = 1

type SecurityConfig struct {
	EnforceAuthenticatedCaller bool
	AllowUnauthenticatedCaller bool
	PerPeerRateLimitPerMin     int
	PerPeerBurst               int
	PerPeerMaxInFlight         int
	GlobalMaxInFlight          int
	RecoveryTimeout            time.Duration
	BreakerFailThreshold       int
	BreakerCooldown            time.Duration
	BreakerMaxHalfOpen         int
}

type peerRateState struct {
	tokens float64
	last   time.Time
}

type actionBreakerState struct {
	failures         int
	openUntil        time.Time
	halfOpenInFlight int
}

type Server struct {
	supernode.UnimplementedSelfHealingServiceServer

	identity       string
	p2p            p2p.Client
	lumera         lumera.Client
	store          queries.LocalStoreInterface
	cascadeFactory cascadeService.CascadeServiceFactory

	actionIndexMu       sync.RWMutex
	actionIndexByFile   map[string]string
	actionIndexLoadedAt time.Time
	actionIndexTTL      time.Duration
	actionIndexRefresh  sync.Mutex
	reseedInFlight      singleflight.Group

	security SecurityConfig

	rateMu           sync.Mutex
	peerRates        map[string]*peerRateState
	inflightMu       sync.Mutex
	inflightByPeer   map[string]int
	globalInFlight   int
	globalInFlightCh chan struct{}
	breakerMu        sync.Mutex
	breakersByAction map[string]*actionBreakerState
}

func NewServer(identity string, p2pClient p2p.Client, lumeraClient lumera.Client, store queries.LocalStoreInterface, securityCfg SecurityConfig, cascadeFactory ...cascadeService.CascadeServiceFactory) *Server {
	var factory cascadeService.CascadeServiceFactory
	if len(cascadeFactory) > 0 {
		factory = cascadeFactory[0]
	}
	securityCfg = normalizeSecurityConfig(securityCfg)
	return &Server{
		identity:          identity,
		p2p:               p2pClient,
		lumera:            lumeraClient,
		store:             store,
		cascadeFactory:    factory,
		actionIndexByFile: make(map[string]string),
		actionIndexTTL:    defaultActionIndexTTL,
		security:          securityCfg,
		peerRates:         make(map[string]*peerRateState),
		inflightByPeer:    make(map[string]int),
		globalInFlightCh:  make(chan struct{}, securityCfg.GlobalMaxInFlight),
		breakersByAction:  make(map[string]*actionBreakerState),
	}
}

func (s *Server) RequestSelfHealing(ctx context.Context, req *supernode.RequestSelfHealingRequest) (*supernode.RequestSelfHealingResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	callerID, _ := reachability.GrpcRemoteIdentityAndAddr(ctx)
	if err := s.validateCallerIdentity(callerID, strings.TrimSpace(req.ChallengerId), "challenger"); err != nil {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       err.Error(),
		}, nil
	}
	if !s.allowPeerRequest(callerID) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "rate limited",
		}, nil
	}
	if strings.TrimSpace(req.ChallengeId) == "" || strings.TrimSpace(req.FileKey) == "" {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "challenge_id and file_key are required",
		}, nil
	}
	if rid := strings.TrimSpace(req.RecipientId); rid != "" && rid != s.identity {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "recipient mismatch",
		}, nil
	}
	if req.EpochId > 0 && s.isStaleEpoch(ctx, req.EpochId) {
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "stale epoch",
		}, nil
	}

	actionID, expectedHashHex, aerr := s.resolveActionAndExpectedHash(ctx, req.ActionId, req.FileKey)
	if aerr != nil {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
			"event":    "response",
			"accepted": false,
			"reason":   "action_context_resolution_failed",
			"error":    aerr.Error(),
		})
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "action resolution failed",
		}, nil
	}

	hashHex := ""
	reconstructionRequired := true
	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	if err == nil && len(data) > 0 {
		sum := blake3.Sum256(data)
		localHashHex := hex.EncodeToString(sum[:])
		if strings.EqualFold(localHashHex, expectedHashHex) {
			hashHex = strings.ToLower(localHashHex)
			reconstructionRequired = false
		}
	}
	if reconstructionRequired {
		reseedRes, rerr := s.runRecoveryReseed(ctx, callerID, actionID, false)
		if rerr != nil {
			reason := classifyReseedError(rerr)
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
				"event":     "response",
				"accepted":  false,
				"reason":    reason,
				"action_id": actionID,
				"error":     rerr.Error(),
			})
			return &supernode.RequestSelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				RecipientId: s.identity,
				Accepted:    false,
				Error:       reason,
			}, nil
		}
		if reseedRes != nil {
			hashHex = strings.ToLower(strings.TrimSpace(reseedRes.ReconstructedHashHex))
		}
		if hashHex == "" {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
				"event":     "response",
				"accepted":  false,
				"reason":    "reconstructed_hash_missing",
				"action_id": actionID,
			})
			return &supernode.RequestSelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				RecipientId: s.identity,
				Accepted:    false,
				Error:       "reconstructed hash missing",
			}, nil
		}
		reconstructionRequired = true
	}
	if !strings.EqualFold(hashHex, expectedHashHex) {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
			"event":              "response",
			"accepted":           false,
			"reason":             "reconstructed_hash_mismatch_action",
			"action_id":          actionID,
			"expected_hash_hex":  expectedHashHex,
			"reconstructed_hash": hashHex,
		})
		return &supernode.RequestSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Accepted:    false,
			Error:       "reconstructed hash mismatch action hash",
		}, nil
	}
	s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{"event": "response", "accepted": true, "reconstruction_required": reconstructionRequired, "reconstructed_hash_hex": hashHex})

	return &supernode.RequestSelfHealingResponse{
		ChallengeId:            req.ChallengeId,
		EpochId:                req.EpochId,
		RecipientId:            s.identity,
		Accepted:               true,
		ReconstructionRequired: reconstructionRequired,
		ReconstructedHashHex:   hashHex,
	}, nil
}

func (s *Server) VerifySelfHealing(ctx context.Context, req *supernode.VerifySelfHealingRequest) (*supernode.VerifySelfHealingResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	callerID, _ := reachability.GrpcRemoteIdentityAndAddr(ctx)
	if err := s.validateCallerIdentity(callerID, strings.TrimSpace(req.ObserverId), "observer"); err != nil {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       err.Error(),
		}, nil
	}
	if !s.allowPeerRequest(callerID) {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "rate limited",
		}, nil
	}
	if strings.TrimSpace(req.ChallengeId) == "" || strings.TrimSpace(req.FileKey) == "" || strings.TrimSpace(req.ReconstructedHashHex) == "" {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "challenge_id, file_key and reconstructed_hash_hex are required",
		}, nil
	}
	if oid := strings.TrimSpace(req.ObserverId); oid != "" && oid != s.identity {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "observer mismatch",
		}, nil
	}
	if req.EpochId > 0 && s.isStaleEpoch(ctx, req.EpochId) {
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "stale epoch",
		}, nil
	}

	actionID, expectedHashHex, aerr := s.resolveActionAndExpectedHash(ctx, req.ActionId, req.FileKey)
	if aerr != nil {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
			"event":    "verification",
			"ok":       false,
			"reason":   "action_context_resolution_failed",
			"error":    aerr.Error(),
			"file_key": req.FileKey,
		})
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "observer action resolution failed",
		}, nil
	}
	if !strings.EqualFold(req.ReconstructedHashHex, expectedHashHex) {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
			"event":              "verification",
			"ok":                 false,
			"reason":             "recipient_hash_mismatch_action",
			"expected_hash_hex":  expectedHashHex,
			"recipient_hash_hex": strings.ToLower(strings.TrimSpace(req.ReconstructedHashHex)),
			"action_id":          actionID,
		})
		return &supernode.VerifySelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			ObserverId:  s.identity,
			Ok:          false,
			Error:       "reconstructed hash does not match action hash",
		}, nil
	}

	got := ""
	needReconstruct := true
	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	if err == nil && len(data) > 0 {
		sum := blake3.Sum256(data)
		localHashHex := hex.EncodeToString(sum[:])
		if strings.EqualFold(localHashHex, expectedHashHex) {
			got = strings.ToLower(localHashHex)
			needReconstruct = false
		}
	}
	if needReconstruct {
		reseedRes, rerr := s.runRecoveryReseed(ctx, callerID, actionID, false)
		if rerr != nil {
			reason := classifyReseedError(rerr)
			s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
				"event":     "verification",
				"ok":        false,
				"reason":    reason,
				"action_id": actionID,
				"error":     rerr.Error(),
			})
			return &supernode.VerifySelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				ObserverId:  s.identity,
				Ok:          false,
				Error:       reason,
			}, nil
		}
		if reseedRes != nil {
			got = strings.ToLower(strings.TrimSpace(reseedRes.ReconstructedHashHex))
		}
		if got == "" {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
				"event":     "verification",
				"ok":        false,
				"reason":    "observer_reconstructed_hash_missing",
				"action_id": actionID,
			})
			return &supernode.VerifySelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				ObserverId:  s.identity,
				Ok:          false,
				Error:       "observer reconstructed hash missing",
			}, nil
		}
	}

	ok := strings.EqualFold(got, req.ReconstructedHashHex) && strings.EqualFold(got, expectedHashHex)
	errMsg := ""
	if !ok {
		errMsg = "reconstructed hash mismatch"
	}
	s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
		"event":               "verification",
		"ok":                  ok,
		"expected":            strings.ToLower(req.ReconstructedHashHex),
		"expected_action":     expectedHashHex,
		"got":                 got,
		"used_reconstruction": needReconstruct,
	})

	return &supernode.VerifySelfHealingResponse{
		ChallengeId: req.ChallengeId,
		EpochId:     req.EpochId,
		ObserverId:  s.identity,
		Ok:          ok,
		Error:       errMsg,
	}, nil
}

func (s *Server) CommitSelfHealing(ctx context.Context, req *supernode.CommitSelfHealingRequest) (*supernode.CommitSelfHealingResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	callerID, _ := reachability.GrpcRemoteIdentityAndAddr(ctx)
	if err := s.validateCallerIdentity(callerID, strings.TrimSpace(req.ChallengerId), "challenger"); err != nil {
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       err.Error(),
		}, nil
	}
	if !s.allowPeerRequest(callerID) {
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "rate limited",
		}, nil
	}
	if strings.TrimSpace(req.ChallengeId) == "" || strings.TrimSpace(req.FileKey) == "" {
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "challenge_id and file_key are required",
		}, nil
	}
	if rid := strings.TrimSpace(req.RecipientId); rid != "" && rid != s.identity {
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "recipient mismatch",
		}, nil
	}
	if req.EpochId > 0 && s.isStaleEpoch(ctx, req.EpochId) {
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "stale epoch",
		}, nil
	}

	actionID, err := s.resolveActionID(ctx, req.ActionId, req.FileKey)
	if err != nil {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingCompletionMessage), map[string]any{
			"event":  "commit",
			"stored": false,
			"reason": "action_resolution_failed",
			"error":  err.Error(),
		})
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "action resolution failed",
		}, nil
	}
	if _, err := s.runRecoveryReseed(ctx, callerID, actionID, true); err != nil {
		reason := classifyReseedError(err)
		s.persistExecution(req.ChallengeId, int(types.SelfHealingCompletionMessage), map[string]any{
			"event":     "commit",
			"stored":    false,
			"reason":    reason,
			"action_id": actionID,
			"error":     err.Error(),
		})
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       reason,
		}, nil
	}

	s.persistExecution(req.ChallengeId, int(types.SelfHealingCompletionMessage), map[string]any{
		"event":     "commit",
		"stored":    true,
		"action_id": actionID,
	})
	return &supernode.CommitSelfHealingResponse{
		ChallengeId: req.ChallengeId,
		EpochId:     req.EpochId,
		RecipientId: s.identity,
		Stored:      true,
	}, nil
}

func (s *Server) persistExecution(challengeID string, msgType int, payload map[string]any) {
	if s.store == nil {
		return
	}
	bz, err := json.Marshal(payload)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	_ = s.store.InsertSelfHealingExecutionMetrics(types.SelfHealingExecutionMetric{
		TriggerID:       challengeID,
		ChallengeID:     challengeID,
		MessageType:     msgType,
		Data:            bz,
		SenderID:        s.identity,
		SenderSignature: []byte{},
		CreatedAt:       now,
		UpdatedAt:       now,
	})
}

func (s *Server) isStaleEpoch(ctx context.Context, reqEpoch uint64) bool {
	if s.lumera == nil || s.lumera.Node() == nil || s.lumera.Audit() == nil {
		return false
	}
	latest, err := s.lumera.Node().GetLatestBlock(ctx)
	if err != nil || latest == nil {
		return false
	}
	var height int64
	if sdkBlk := latest.GetSdkBlock(); sdkBlk != nil {
		height = sdkBlk.Header.Height
	} else if blk := latest.GetBlock(); blk != nil {
		height = blk.Header.Height
	}
	if height <= 0 {
		return false
	}
	paramsResp, err := s.lumera.Audit().GetParams(ctx)
	if err != nil || paramsResp == nil {
		return false
	}
	p := paramsResp.Params.WithDefaults()
	if err := p.Validate(); err != nil {
		return false
	}
	current, ok := deterministic.EpochID(height, p.EpochZeroHeight, p.EpochLengthBlocks)
	if !ok {
		return false
	}
	if reqEpoch > current+epochSkewTolerance {
		return true
	}
	if current > reqEpoch+epochSkewTolerance {
		return true
	}
	return false
}

func (s *Server) resolveActionIDByFileKey(ctx context.Context, fileKey string) (string, error) {
	key := strings.TrimSpace(fileKey)
	if key == "" {
		return "", fmt.Errorf("empty file key")
	}

	if actionID, ok := s.getActionIDFromIndex(key); ok {
		return actionID, nil
	}

	if err := s.refreshActionIndexIfNeeded(ctx, false); err != nil {
		return "", err
	}
	if actionID, ok := s.getActionIDFromIndex(key); ok {
		return actionID, nil
	}

	// Force one fresh on-chain pull to avoid stale-cache misses.
	if err := s.refreshActionIndexIfNeeded(ctx, true); err != nil {
		return "", err
	}
	if actionID, ok := s.getActionIDFromIndex(key); ok {
		return actionID, nil
	}

	return "", fmt.Errorf("cascade action not found for key")
}

func (s *Server) resolveActionAndExpectedHash(ctx context.Context, actionID string, fileKey string) (string, string, error) {
	resolvedActionID, err := s.resolveActionID(ctx, actionID, fileKey)
	if err != nil {
		return "", "", err
	}
	expectedHashHex, err := s.resolveActionDataHashHex(ctx, resolvedActionID)
	if err != nil {
		return "", "", err
	}
	return resolvedActionID, expectedHashHex, nil
}

func (s *Server) resolveActionDataHashHex(ctx context.Context, actionID string) (string, error) {
	actionID = strings.TrimSpace(actionID)
	if actionID == "" {
		return "", fmt.Errorf("missing action_id")
	}
	if s.lumera == nil || s.lumera.Action() == nil {
		return "", fmt.Errorf("action module unavailable")
	}
	resp, err := s.lumera.Action().GetAction(ctx, actionID)
	if err != nil {
		return "", fmt.Errorf("get action: %w", err)
	}
	if resp == nil || resp.Action == nil || len(resp.Action.Metadata) == 0 {
		return "", fmt.Errorf("cascade action metadata unavailable")
	}
	meta, err := cascadekit.UnmarshalCascadeMetadata(resp.Action.Metadata)
	if err != nil {
		return "", err
	}
	dataHashB64 := strings.TrimSpace(meta.DataHash)
	if dataHashB64 == "" {
		return "", fmt.Errorf("action metadata data hash missing")
	}
	raw, err := base64.StdEncoding.DecodeString(dataHashB64)
	if err != nil {
		return "", fmt.Errorf("decode action data hash: %w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("action data hash empty")
	}
	return strings.ToLower(hex.EncodeToString(raw)), nil
}

func (s *Server) resolveActionID(ctx context.Context, actionID string, fileKey string) (string, error) {
	actionID = strings.TrimSpace(actionID)
	if actionID != "" {
		return actionID, nil
	}
	return s.resolveActionIDByFileKey(ctx, fileKey)
}

func (s *Server) getActionIDFromIndex(fileKey string) (string, bool) {
	s.actionIndexMu.RLock()
	defer s.actionIndexMu.RUnlock()
	actionID, ok := s.actionIndexByFile[fileKey]
	return actionID, ok
}

func (s *Server) refreshActionIndexIfNeeded(ctx context.Context, force bool) error {
	s.actionIndexRefresh.Lock()
	defer s.actionIndexRefresh.Unlock()

	if !force {
		s.actionIndexMu.RLock()
		fresh := time.Since(s.actionIndexLoadedAt) <= s.actionIndexTTL && len(s.actionIndexByFile) > 0
		s.actionIndexMu.RUnlock()
		if fresh {
			return nil
		}
	}

	if s.lumera == nil || s.lumera.Action() == nil {
		return fmt.Errorf("action module unavailable")
	}

	states := []actiontypes.ActionState{
		actiontypes.ActionStateDone,
		actiontypes.ActionStateApproved,
	}
	newIndex := make(map[string]string)
	for _, state := range states {
		var nextKey []byte
		for {
			resp, err := s.lumera.Action().ListActions(ctx, &actiontypes.QueryListActionsRequest{
				ActionType:  actiontypes.ActionTypeCascade,
				ActionState: state,
				Pagination: &query.PageRequest{
					Key:   nextKey,
					Limit: defaultActionPageLimit,
				},
			})
			if err != nil {
				return fmt.Errorf("list cascade actions (state=%s): %w", state.String(), err)
			}
			if resp == nil {
				break
			}
			for _, action := range resp.Actions {
				if action == nil {
					continue
				}
				actionID := strings.TrimSpace(action.ActionID)
				if actionID == "" || len(action.Metadata) == 0 {
					continue
				}
				meta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
				if err != nil {
					continue
				}
				anchor := pickActionAnchorKey(meta.RqIdsIds)
				if anchor == "" {
					continue
				}
				newIndex[anchor] = actionID
			}
			if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
				break
			}
			nextKey = append(nextKey[:0], resp.Pagination.NextKey...)
		}
	}

	s.actionIndexMu.Lock()
	s.actionIndexByFile = newIndex
	s.actionIndexLoadedAt = time.Now().UTC()
	s.actionIndexMu.Unlock()
	return nil
}

func (s *Server) runRecoveryReseed(ctx context.Context, callerID string, actionID string, persistArtifacts bool) (*cascadeService.RecoveryReseedResult, error) {
	actionID = strings.TrimSpace(actionID)
	if actionID == "" {
		return nil, fmt.Errorf("missing action_id")
	}
	release, err := s.acquireHeavyPath(callerID, actionID)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() { release(success) }()

	callKey := fmt.Sprintf("%s:%t", actionID, persistArtifacts)
	result, err, _ := s.reseedInFlight.Do(callKey, func() (any, error) {
		if s.cascadeFactory == nil {
			return nil, fmt.Errorf("recovery reseed unavailable")
		}
		reseedCtx, cancel := context.WithTimeout(ctx, s.security.RecoveryTimeout)
		defer cancel()

		task := s.cascadeFactory.NewCascadeRegistrationTask()
		if task == nil {
			return nil, fmt.Errorf("failed to build cascade task")
		}
		recoveryTask, ok := any(task).(interface {
			RecoveryReseed(context.Context, *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error)
		})
		if !ok {
			return nil, fmt.Errorf("cascade task does not support recovery reseed")
		}
		reseedRes, err := recoveryTask.RecoveryReseed(reseedCtx, &cascadeService.RecoveryReseedRequest{
			ActionID:         actionID,
			PersistArtifacts: boolPtr(persistArtifacts),
		})
		if err != nil {
			return nil, err
		}
		return reseedRes, nil
	})
	if err != nil {
		return nil, err
	}
	if typed, ok := result.(*cascadeService.RecoveryReseedResult); ok {
		success = true
		return typed, nil
	}
	return nil, fmt.Errorf("unexpected recovery result type")
}

func boolPtr(v bool) *bool {
	return &v
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

func normalizeSecurityConfig(cfg SecurityConfig) SecurityConfig {
	if !cfg.EnforceAuthenticatedCaller && !cfg.AllowUnauthenticatedCaller {
		cfg.EnforceAuthenticatedCaller = true
	}
	if cfg.PerPeerRateLimitPerMin <= 0 {
		cfg.PerPeerRateLimitPerMin = defaultPerPeerRateLimitPerMin
	}
	if cfg.PerPeerBurst <= 0 {
		cfg.PerPeerBurst = defaultPerPeerBurst
	}
	if cfg.PerPeerMaxInFlight <= 0 {
		cfg.PerPeerMaxInFlight = defaultPerPeerMaxInFlight
	}
	if cfg.GlobalMaxInFlight <= 0 {
		cfg.GlobalMaxInFlight = defaultGlobalMaxInFlight
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = defaultRecoveryTimeout
	}
	if cfg.BreakerFailThreshold <= 0 {
		cfg.BreakerFailThreshold = defaultBreakerFailThreshold
	}
	if cfg.BreakerCooldown <= 0 {
		cfg.BreakerCooldown = defaultBreakerCooldown
	}
	if cfg.BreakerMaxHalfOpen <= 0 {
		cfg.BreakerMaxHalfOpen = defaultBreakerHalfOpenPermits
	}
	return cfg
}

func (s *Server) allowPeerRequest(peerID string) bool {
	if s.security.PerPeerRateLimitPerMin <= 0 {
		return true
	}
	now := time.Now().UTC()
	key := peerIDKey(peerID)
	ratePerSec := float64(s.security.PerPeerRateLimitPerMin) / 60.0
	if ratePerSec <= 0 {
		return true
	}

	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	st, ok := s.peerRates[key]
	if !ok {
		st = &peerRateState{
			tokens: float64(s.security.PerPeerBurst - 1),
			last:   now,
		}
		s.peerRates[key] = st
		return true
	}
	elapsed := now.Sub(st.last).Seconds()
	if elapsed > 0 {
		st.tokens = math.Min(float64(s.security.PerPeerBurst), st.tokens+elapsed*ratePerSec)
		st.last = now
	}
	if st.tokens < 1.0 {
		return false
	}
	st.tokens -= 1.0
	return true
}

func (s *Server) validateCallerIdentity(callerID, expectedID, role string) error {
	expectedID = strings.TrimSpace(expectedID)
	callerID = strings.TrimSpace(callerID)
	if expectedID == "" {
		return fmt.Errorf("%s identity is required", role)
	}
	if callerID == "" {
		if s.security.EnforceAuthenticatedCaller && !s.security.AllowUnauthenticatedCaller {
			return fmt.Errorf("unauthenticated caller")
		}
		return nil
	}
	if !strings.EqualFold(callerID, expectedID) {
		return fmt.Errorf("caller identity mismatch")
	}
	return nil
}

func (s *Server) acquireHeavyPath(peerID, actionID string) (func(success bool), error) {
	peerKey := peerIDKey(peerID)

	if !s.enterBreaker(actionID) {
		return nil, fmt.Errorf("circuit open")
	}

	if !s.acquirePeerInFlight(peerKey) {
		s.leaveBreakerHalfOpen(actionID)
		return nil, fmt.Errorf("peer concurrency limit")
	}

	select {
	case s.globalInFlightCh <- struct{}{}:
	default:
		s.releasePeerInFlight(peerKey)
		s.leaveBreakerHalfOpen(actionID)
		return nil, fmt.Errorf("global concurrency limit")
	}

	return func(success bool) {
		<-s.globalInFlightCh
		s.releasePeerInFlight(peerKey)
		s.completeBreaker(actionID, success)
	}, nil
}

func (s *Server) acquirePeerInFlight(peerKey string) bool {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	current := s.inflightByPeer[peerKey]
	if current >= s.security.PerPeerMaxInFlight {
		return false
	}
	s.inflightByPeer[peerKey] = current + 1
	s.globalInFlight++
	return true
}

func (s *Server) releasePeerInFlight(peerKey string) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	current := s.inflightByPeer[peerKey]
	if current <= 1 {
		delete(s.inflightByPeer, peerKey)
	} else {
		s.inflightByPeer[peerKey] = current - 1
	}
	if s.globalInFlight > 0 {
		s.globalInFlight--
	}
}

func (s *Server) enterBreaker(actionID string) bool {
	key := strings.TrimSpace(actionID)
	if key == "" {
		return true
	}
	now := time.Now().UTC()
	s.breakerMu.Lock()
	defer s.breakerMu.Unlock()

	st, ok := s.breakersByAction[key]
	if !ok {
		st = &actionBreakerState{}
		s.breakersByAction[key] = st
	}
	if st.openUntil.After(now) {
		return false
	}
	if st.failures >= s.security.BreakerFailThreshold {
		if st.halfOpenInFlight >= s.security.BreakerMaxHalfOpen {
			return false
		}
		st.halfOpenInFlight++
	}
	return true
}

func (s *Server) leaveBreakerHalfOpen(actionID string) {
	key := strings.TrimSpace(actionID)
	if key == "" {
		return
	}
	s.breakerMu.Lock()
	defer s.breakerMu.Unlock()
	st, ok := s.breakersByAction[key]
	if !ok {
		return
	}
	if st.halfOpenInFlight > 0 {
		st.halfOpenInFlight--
	}
}

func (s *Server) completeBreaker(actionID string, success bool) {
	key := strings.TrimSpace(actionID)
	if key == "" {
		return
	}
	now := time.Now().UTC()
	s.breakerMu.Lock()
	defer s.breakerMu.Unlock()

	st, ok := s.breakersByAction[key]
	if !ok {
		return
	}
	if st.halfOpenInFlight > 0 {
		st.halfOpenInFlight--
	}
	if success {
		st.failures = 0
		st.openUntil = time.Time{}
		return
	}
	st.failures++
	if st.failures >= s.security.BreakerFailThreshold {
		st.openUntil = now.Add(s.security.BreakerCooldown)
	}
}

func peerIDKey(peerID string) string {
	peerID = strings.TrimSpace(strings.ToLower(peerID))
	if peerID == "" {
		return "unknown"
	}
	return peerID
}

func classifyReseedError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "circuit open"):
		return "circuit_open"
	case strings.Contains(msg, "rate limit"):
		return "rate_limited"
	case strings.Contains(msg, "peer concurrency limit"):
		return "peer_concurrency_limited"
	case strings.Contains(msg, "global concurrency limit"):
		return "global_concurrency_limited"
	case strings.Contains(msg, "context deadline exceeded"):
		return "reconstruction_timeout"
	default:
		return "reconstruction_failed"
	}
}
