package storage_challenge

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"lukechampine.com/blake3"
)

const maxServedSliceBytes = uint64(65_536)

// ArtifactReader is the recipient-side abstraction over cascade artifact storage
// used to satisfy LEP-6 multi-range compound storage challenges. The B.3 wiring
// will provide a cascade-module-backed implementation; tests inject their own.
type ArtifactReader interface {
	ReadArtifactRange(ctx context.Context, class audittypes.StorageProofArtifactClass, key string, start, end uint64) ([]byte, error)
}

type Server struct {
	supernode.UnimplementedStorageChallengeServiceServer

	identity string
	p2p      p2p.Client
	store    queries.LocalStoreInterface
	reader   ArtifactReader

	// keyring + keyName are used to sign LEP-6 GetCompoundProof responses
	// (recipient_signature) over the response transcript hash. Both may
	// remain unset for legacy / test paths; signing is then skipped and
	// recipient_signature stays empty.
	keyring keyring.Keyring
	keyName string
}

func NewServer(identity string, p2pClient p2p.Client, store queries.LocalStoreInterface) *Server {
	return &Server{identity: identity, p2p: p2pClient, store: store}
}

// WithArtifactReader configures the server with the LEP-6 compound-challenge
// recipient-side reader. Returns the receiver for chained construction.
func (s *Server) WithArtifactReader(reader ArtifactReader) *Server {
	s.reader = reader
	return s
}

// WithRecipientSigner configures the keyring + key name used to sign
// LEP-6 GetCompoundProof response transcripts. Returns the receiver for
// chained construction.
func (s *Server) WithRecipientSigner(kr keyring.Keyring, keyName string) *Server {
	s.keyring = kr
	s.keyName = keyName
	return s
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
		return &supernode.GetSliceProofResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			FileKey:     req.FileKey,
			RecipientId: s.identity,
			Ok:          false,
			Error:       "invalid requested range: requested_end must be greater than requested_start",
		}, nil
	}
	dataLen := uint64(len(data))
	if start >= dataLen {
		return &supernode.GetSliceProofResponse{
			ChallengeId: req.ChallengeId,
			EpochId:     req.EpochId,
			FileKey:     req.FileKey,
			RecipientId: s.identity,
			Ok:          false,
			Error:       "invalid requested range: requested_start is out of bounds",
		}, nil
	}
	if end > dataLen {
		end = dataLen
	}
	if end-start > maxServedSliceBytes {
		end = start + maxServedSliceBytes
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
	ok := strings.EqualFold(got, want)
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
		RecipientID:  s.identity,
		Observers:    append([]string(nil), req.ObserverIds...),
		Challenge: types.ChallengeData{
			FileHash:   req.FileKey,
			StartIndex: int(resp.Start),
			EndIndex:   int(resp.End),
			Timestamp:  time.Now().UTC(),
		},
	}
	challengeBz, err := json.Marshal(challenge)
	if err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to marshal challenge message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	} else if err := s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.ChallengeMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            challengeBz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	}); err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to persist challenge message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	}

	response := types.MessageData{
		ChallengerID: req.ChallengerId,
		RecipientID:  s.identity,
		Observers:    append([]string(nil), req.ObserverIds...),
		Response: types.ResponseData{
			Hash:      resp.ProofHashHex,
			Timestamp: time.Now().UTC(),
		},
	}
	responseBz, err := json.Marshal(response)
	if err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to marshal response message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	} else if err := s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.ResponseMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            responseBz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	}); err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to persist response message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	}

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
	bz, err := json.Marshal(eval)
	if err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to marshal affirmation message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	} else if err := s.store.InsertStorageChallengeMessage(types.StorageChallengeLogMessage{
		MessageType:     int(types.AffirmationMessageType),
		ChallengeID:     req.ChallengeId,
		Data:            bz,
		Sender:          s.identity,
		SenderSignature: []byte{},
	}); err != nil {
		logtrace.Warn(ctx, "storage challenge: failed to persist affirmation message", logtrace.Fields{
			"challenge_id": req.ChallengeId,
			"error":        err,
		})
	}

	logtrace.Debug(ctx, "storage challenge proof verified", logtrace.Fields{
		"challenge_id": req.ChallengeId,
		"ok":           resp.Ok,
	})
}

// GetCompoundProof serves a LEP-6 multi-range compound storage challenge.
// The challenger derives range count and range size from chain params; the
// recipient therefore validates only request-level structural invariants rather
// than re-asserting local compile-time defaults. It reads the requested ranges
// via the injected ArtifactReader, computes a BLAKE3 hash over the
// concatenation, and returns range_bytes alongside the proof hash.
func (s *Server) GetCompoundProof(ctx context.Context, req *supernode.GetCompoundProofRequest) (*supernode.GetCompoundProofResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	resp := &supernode.GetCompoundProofResponse{
		ChallengeId:     req.ChallengeId,
		EpochId:         req.EpochId,
		TicketId:        req.TicketId,
		ArtifactClass:   req.ArtifactClass,
		ArtifactOrdinal: req.ArtifactOrdinal,
		BucketType:      req.BucketType,
		ArtifactKey:     req.ArtifactKey,
	}

	if req.ChallengeId == "" {
		resp.Error = "challenge_id is required"
		return resp, nil
	}
	if req.EpochId == 0 {
		resp.Error = "epoch_id must be > 0"
		return resp, nil
	}
	if req.TicketId == "" {
		resp.Error = "ticket_id is required"
		return resp, nil
	}
	if len(req.Ranges) == 0 {
		resp.Error = "at least one range is required"
		return resp, nil
	}
	var requestRangeLen uint64
	for i, rng := range req.Ranges {
		if rng == nil {
			resp.Error = fmt.Sprintf("range[%d] is nil", i)
			return resp, nil
		}
		if rng.End <= rng.Start {
			resp.Error = fmt.Sprintf("range[%d] invalid: end (%d) must be > start (%d)", i, rng.End, rng.Start)
			return resp, nil
		}
		size := rng.End - rng.Start
		if i == 0 {
			requestRangeLen = size
		} else if size != requestRangeLen {
			resp.Error = fmt.Sprintf("range[%d] invalid size: got %d, want %d from first range", i, size, requestRangeLen)
			return resp, nil
		}
		if rng.End > req.ArtifactSize {
			resp.Error = fmt.Sprintf("range[%d] out of bounds: end (%d) > artifact_size (%d)", i, rng.End, req.ArtifactSize)
			return resp, nil
		}
	}

	if s.reader == nil {
		resp.Error = "artifact reader not configured"
		return resp, nil
	}

	class := audittypes.StorageProofArtifactClass(req.ArtifactClass)
	rangeBytes := make([][]byte, 0, len(req.Ranges))
	hasher := blake3.New(32, nil)
	for i, rng := range req.Ranges {
		buf, err := s.reader.ReadArtifactRange(ctx, class, req.ArtifactKey, rng.Start, rng.End)
		if err != nil {
			resp.Error = fmt.Sprintf("read range[%d] [%d,%d): %v", i, rng.Start, rng.End, err)
			return resp, nil
		}
		rangeBytes = append(rangeBytes, buf)
		_, _ = hasher.Write(buf)
	}
	sum := hasher.Sum(nil)
	resp.RangeBytes = rangeBytes
	resp.ProofHashHex = hex.EncodeToString(sum)

	// Sign the response transcript with the recipient's keyring identity.
	// The transcript composition mirrors the challenger-side TranscriptHash
	// composition (deterministic.TranscriptInputs) so the off-chain
	// reporter can attach this signature to its StorageProofResult and
	// the chain (post-LEP-6) can verify both endpoints corroborate the
	// proof. Recipient acts here as the TARGET supernode.
	if s.keyring != nil && strings.TrimSpace(s.keyName) != "" {
		obs := append([]string(nil), req.ObserverAccounts...)
		offsets := make([]uint64, 0, len(req.Ranges))
		for _, rng := range req.Ranges {
			offsets = append(offsets, rng.Start)
		}
		derivHash, hashErr := deterministic.DerivationInputHash(req.Seed, req.TargetSupernodeAccount, req.TicketId, class, req.ArtifactOrdinal, offsets, requestRangeLen)
		if hashErr != nil {
			resp.Error = fmt.Sprintf("derivation input hash: %v", hashErr)
			return resp, nil
		}
		txHash, hashErr := deterministic.TranscriptHash(deterministic.TranscriptInputs{
			EpochID:                    req.EpochId,
			ChallengerSupernodeAccount: req.ChallengerAccount,
			TargetSupernodeAccount:     req.TargetSupernodeAccount,
			TicketID:                   req.TicketId,
			Bucket:                     audittypes.StorageProofBucketType(req.BucketType),
			ArtifactClass:              class,
			ArtifactOrdinal:            req.ArtifactOrdinal,
			ArtifactKey:                req.ArtifactKey,
			DerivationInputHash:        derivHash,
			CompoundProofHashHex:       resp.ProofHashHex,
			ObserverIDs:                obs,
		})
		if hashErr != nil {
			resp.Error = fmt.Sprintf("transcript hash: %v", hashErr)
			return resp, nil
		}
		sig, signErr := snkeyring.SignBytes(s.keyring, s.keyName, []byte(txHash))
		if signErr != nil {
			resp.Error = fmt.Sprintf("recipient sign: %v", signErr)
			return resp, nil
		}
		resp.RecipientSignature = hex.EncodeToString(sig)
	}
	resp.Ok = true
	return resp, nil
}
