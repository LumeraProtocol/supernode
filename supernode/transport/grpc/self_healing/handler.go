package self_healing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
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
}

func NewServer(identity string, p2pClient p2p.Client, lumeraClient lumera.Client, store queries.LocalStoreInterface, cascadeFactory ...cascadeService.CascadeServiceFactory) *Server {
	var factory cascadeService.CascadeServiceFactory
	if len(cascadeFactory) > 0 {
		factory = cascadeFactory[0]
	}
	return &Server{
		identity:          identity,
		p2p:               p2pClient,
		lumera:            lumeraClient,
		store:             store,
		cascadeFactory:    factory,
		actionIndexByFile: make(map[string]string),
		actionIndexTTL:    defaultActionIndexTTL,
	}
}

func (s *Server) RequestSelfHealing(ctx context.Context, req *supernode.RequestSelfHealingRequest) (*supernode.RequestSelfHealingResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
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

	hashHex := ""
	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	reconstructionRequired := false
	if err == nil && len(data) > 0 {
		sum := blake3.Sum256(data)
		hashHex = hex.EncodeToString(sum[:])
	} else {
		actionID, rerr := s.resolveActionID(ctx, req.ActionId, req.FileKey)
		if rerr != nil {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
				"event":    "response",
				"accepted": false,
				"reason":   "action_resolution_failed",
				"error":    rerr.Error(),
			})
			return &supernode.RequestSelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				RecipientId: s.identity,
				Accepted:    false,
				Error:       "action resolution failed",
			}, nil
		}
		reseedRes, rerr := s.runRecoveryReseed(ctx, actionID, false)
		if rerr != nil {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{
				"event":     "response",
				"accepted":  false,
				"reason":    "reconstruction_failed",
				"action_id": actionID,
				"error":     rerr.Error(),
			})
			return &supernode.RequestSelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				RecipientId: s.identity,
				Accepted:    false,
				Error:       "reconstruction failed",
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

	got := ""
	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	if err == nil && len(data) > 0 {
		sum := blake3.Sum256(data)
		got = hex.EncodeToString(sum[:])
	} else {
		actionID, rerr := s.resolveActionID(ctx, req.ActionId, req.FileKey)
		if rerr != nil {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
				"event":    "verification",
				"ok":       false,
				"reason":   "action_resolution_failed",
				"file_key": req.FileKey,
				"error":    rerr.Error(),
			})
			return &supernode.VerifySelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				ObserverId:  s.identity,
				Ok:          false,
				Error:       "observer action resolution failed",
			}, nil
		}
		reseedRes, rerr := s.runRecoveryReseed(ctx, actionID, false)
		if rerr != nil {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{
				"event":     "verification",
				"ok":        false,
				"reason":    "observer_reconstruction_failed",
				"action_id": actionID,
				"error":     rerr.Error(),
			})
			return &supernode.VerifySelfHealingResponse{
				ChallengeId: req.ChallengeId,
				EpochId:     req.EpochId,
				ObserverId:  s.identity,
				Ok:          false,
				Error:       "observer reconstruction failed",
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

	ok := strings.EqualFold(got, req.ReconstructedHashHex)
	errMsg := ""
	if !ok {
		errMsg = "reconstructed hash mismatch"
	}
	s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{"event": "verification", "ok": ok, "expected": strings.ToLower(req.ReconstructedHashHex), "got": got})

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
	if _, err := s.runRecoveryReseed(ctx, actionID, true); err != nil {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingCompletionMessage), map[string]any{
			"event":     "commit",
			"stored":    false,
			"reason":    "store_failed",
			"action_id": actionID,
			"error":     err.Error(),
		})
		return &supernode.CommitSelfHealingResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			RecipientId: s.identity,
			Stored:      false,
			Error:       "artifact store failed",
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

func (s *Server) runRecoveryReseed(ctx context.Context, actionID string, persistArtifacts bool) (*cascadeService.RecoveryReseedResult, error) {
	actionID = strings.TrimSpace(actionID)
	if actionID == "" {
		return nil, fmt.Errorf("missing action_id")
	}
	callKey := fmt.Sprintf("%s:%t", actionID, persistArtifacts)
	result, err, _ := s.reseedInFlight.Do(callKey, func() (any, error) {
		if s.cascadeFactory == nil {
			return nil, fmt.Errorf("recovery reseed unavailable")
		}
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
		reseedRes, err := recoveryTask.RecoveryReseed(ctx, &cascadeService.RecoveryReseedRequest{
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
