package self_healing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"lukechampine.com/blake3"
)

const epochSkewTolerance = uint64(2)

type Server struct {
	supernode.UnimplementedSelfHealingServiceServer

	identity string
	p2p      p2p.Client
	lumera   lumera.Client
	store    queries.LocalStoreInterface
}

func NewServer(identity string, p2pClient p2p.Client, lumeraClient lumera.Client, store queries.LocalStoreInterface) *Server {
	return &Server{identity: identity, p2p: p2pClient, lumera: lumeraClient, store: store}
}

func (s *Server) RequestSelfHealing(ctx context.Context, req *supernode.RequestSelfHealingRequest) (*supernode.RequestSelfHealingResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	if strings.TrimSpace(req.ChallengeId) == "" || strings.TrimSpace(req.FileKey) == "" {
		return &supernode.RequestSelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, RecipientId: s.identity, Accepted: false, Error: "challenge_id and file_key are required"}, nil
	}
	if req.EpochId > 0 && s.isStaleEpoch(ctx, req.EpochId) {
		return &supernode.RequestSelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, RecipientId: s.identity, Accepted: false, Error: "stale epoch"}, nil
	}

	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	reconstructionRequired := false
	if err != nil || len(data) == 0 {
		data, err = s.p2p.Retrieve(ctx, req.FileKey)
		if err != nil || len(data) == 0 {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{"event": "response", "accepted": false, "reason": "file_not_retrievable"})
			return &supernode.RequestSelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, RecipientId: s.identity, Accepted: false, Error: "file not retrievable"}, nil
		}
		reconstructionRequired = true
		if _, err := s.p2p.LocalStore(ctx, req.FileKey, data); err != nil {
			s.persistExecution(req.ChallengeId, int(types.SelfHealingResponseMessage), map[string]any{"event": "response", "accepted": false, "reason": "local_store_failed", "error": err.Error()})
			return &supernode.RequestSelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, RecipientId: s.identity, Accepted: false, Error: "local store failed"}, nil
		}
	}

	sum := blake3.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])
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
		return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, ObserverId: s.identity, Ok: false, Error: "challenge_id, file_key and reconstructed_hash_hex are required"}, nil
	}
	if req.EpochId > 0 && s.isStaleEpoch(ctx, req.EpochId) {
		return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, ObserverId: s.identity, Ok: false, Error: "stale epoch"}, nil
	}

	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	if err != nil || len(data) == 0 {
		s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{"event": "verification", "ok": false, "reason": "file_missing_local"})
		return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, ObserverId: s.identity, Ok: false, Error: "observer missing local file"}, nil
	}

	sum := blake3.Sum256(data)
	got := hex.EncodeToString(sum[:])
	ok := strings.EqualFold(got, req.ReconstructedHashHex)
	errMsg := ""
	if !ok {
		errMsg = "reconstructed hash mismatch"
	}
	s.persistExecution(req.ChallengeId, int(types.SelfHealingVerificationMessage), map[string]any{"event": "verification", "ok": ok, "expected": strings.ToLower(req.ReconstructedHashHex), "got": got})

	return &supernode.VerifySelfHealingResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, ObserverId: s.identity, Ok: ok, Error: errMsg}, nil
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
