package storage_challenge

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"lukechampine.com/blake3"
)

type Server struct {
	supernode.UnimplementedStorageChallengeServiceServer

	identity string
	p2p      p2p.Client
	store    queries.LocalStoreInterface
}

func NewServer(identity string, p2pClient p2p.Client, store queries.LocalStoreInterface) *Server {
	return &Server{identity: identity, p2p: p2pClient, store: store}
}

func (s *Server) GetSliceProof(ctx context.Context, req *supernode.GetSliceProofRequest) (*supernode.GetSliceProofResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	if req.FileKey == "" {
		return &supernode.GetSliceProofResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, Ok: false, Error: "file_key is required"}, nil
	}

	data, err := s.p2p.Retrieve(ctx, req.FileKey, true)
	if err != nil {
		return &supernode.GetSliceProofResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, FileKey: req.FileKey, RecipientId: s.identity, Ok: false, Error: err.Error()}, nil
	}
	if len(data) == 0 {
		return &supernode.GetSliceProofResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, FileKey: req.FileKey, RecipientId: s.identity, Ok: false, Error: "file not found"}, nil
	}

	start := req.RequestedStart
	end := req.RequestedEnd
	if end <= start {
		start = 0
		end = uint64(len(data))
	}
	if start >= uint64(len(data)) {
		start = 0
	}
	if end > uint64(len(data)) {
		end = uint64(len(data))
	}
	if end < start {
		end = start
	}

	slice := make([]byte, int(end-start))
	copy(slice, data[start:end])
	sum := blake3.Sum256(slice)
	proofHex := hex.EncodeToString(sum[:])

	resp := &supernode.GetSliceProofResponse{
		ChallengeId:  req.ChallengeId,
		EpochId:      req.EpochId,
		FileKey:      req.FileKey,
		Start:        start,
		End:          end,
		RecipientId:  s.identity,
		Slice:        slice,
		ProofHashHex: proofHex,
		Ok:           true,
	}

	s.persistRecipientProof(ctx, req, resp)
	return resp, nil
}

func (s *Server) VerifySliceProof(ctx context.Context, req *supernode.VerifySliceProofRequest) (*supernode.VerifySliceProofResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	if req.ProofHashHex == "" {
		return &supernode.VerifySliceProofResponse{ChallengeId: req.ChallengeId, EpochId: req.EpochId, ObserverId: s.identity, Ok: false, Error: "proof_hash_hex is required"}, nil
	}

	sum := blake3.Sum256(req.Slice)
	want := req.ProofHashHex
	got := hex.EncodeToString(sum[:])
	ok := got == want
	errStr := ""
	if !ok {
		errStr = fmt.Sprintf("proof mismatch: want=%s got=%s", want, got)
	}

	resp := &supernode.VerifySliceProofResponse{
		ChallengeId: req.ChallengeId,
		EpochId:     req.EpochId,
		ObserverId:  s.identity,
		Ok:          ok,
		Error:       errStr,
	}
	s.persistObserverVerification(ctx, req, resp)
	return resp, nil
}

func (s *Server) persistRecipientProof(ctx context.Context, req *supernode.GetSliceProofRequest, resp *supernode.GetSliceProofResponse) {
	if s.store == nil {
		return
	}

	challenge := types.MessageData{
		ChallengerID: req.ChallengerId,
		RecipientID:  req.RecipientId,
		Observers:    append([]string(nil), req.ObserverIds...),
		Challenge: types.ChallengeData{
			FileHash:   req.FileKey,
			StartIndex: int(req.RequestedStart),
			EndIndex:   int(req.RequestedEnd),
			Timestamp:  time.Now().UTC(),
		},
	}
	challengeBz, _ := json.Marshal(challenge)
	_ = s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.ChallengeMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            challengeBz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	})

	response := types.MessageData{
		ChallengerID: req.ChallengerId,
		RecipientID:  req.RecipientId,
		Observers:    append([]string(nil), req.ObserverIds...),
		Response: types.ResponseData{
			Hash:      resp.ProofHashHex,
			Timestamp: time.Now().UTC(),
		},
	}
	responseBz, _ := json.Marshal(response)
	_ = s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.ResponseMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            responseBz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	})

	logtrace.Debug(ctx, "storage challenge proof served", logtrace.Fields{
		"challenge_id": req.ChallengeId,
		"file_key":     req.FileKey,
		"start":        resp.Start,
		"end":          resp.End,
	})
}

func (s *Server) persistObserverVerification(ctx context.Context, req *supernode.VerifySliceProofRequest, resp *supernode.VerifySliceProofResponse) {
	if s.store == nil {
		return
	}

	eval := types.MessageData{
		ChallengerID: req.ChallengerId,
		RecipientID:  req.RecipientId,
		Observers:    []string{s.identity},
		ObserverEvaluation: types.ObserverEvaluationData{
			IsEvaluationResultOK: resp.Ok,
			Reason:               resp.Error,
			TrueHash:             req.ProofHashHex,
			Timestamp:            time.Now().UTC(),
		},
	}
	bz, _ := json.Marshal(eval)
	_ = s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.AffirmationMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            bz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	})

	logtrace.Debug(ctx, "storage challenge proof verified", logtrace.Fields{
		"challenge_id": req.ChallengeId,
		"ok":           resp.Ok,
	})
}
