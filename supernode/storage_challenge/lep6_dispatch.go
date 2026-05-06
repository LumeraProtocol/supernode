package storage_challenge

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"lukechampine.com/blake3"
)

// LEP6 dispatcher — challenger-side per-epoch loop for the LEP-6 compound
// storage challenge. See docs/plans/LEP6_SUPERNODE_IMPLEMENTATION_PLAN_v2.md
// §2.3 (PR3) for full design rationale and §9-§11 of LEP6.md for the
// deterministic protocol surfaces.
//
// PR3 scope:
//   - Reads EpochAnchor + assigned targets + audit Params (mode gate +
//     bucket thresholds + multi-range params).
//   - For each (target, bucket ∈ {RECENT, OLD}) deterministically selects
//     ticket / artifact / ordinal / ranges.
//   - Issues GetCompoundProof to the target via SupernodeClientFactory.
//   - Locally recomputes the BLAKE3 proof hash, classifies PASS/FAIL,
//     signs the transcript, and appends the StorageProofResult to the
//     buffer for the host reporter to drain.
//
// PR3 does NOT cover:
//   - Observer attestation collection (post-LEP-6 work).
//   - RECHECK bucket dispatch (PR5 recheck service).
//   - Probation/heal-op exclusion semantics.
//
// Ticket discovery is delegated to a TicketProvider interface. Production
// startup wires ChainTicketProvider, backed by x/action ListActionsBySuperNode;
// NoTicketProvider is retained only for tests and defensive fallback.

// SupernodeCompoundClient is the minimal RPC surface the dispatcher needs
// to drive a target's recipient handler. The real implementation wraps the
// secure gRPC stub (gen/supernode.StorageChallengeServiceClient); tests
// inject a stub directly.
type SupernodeCompoundClient interface {
	GetCompoundProof(ctx context.Context, req *supernode.GetCompoundProofRequest) (*supernode.GetCompoundProofResponse, error)
	Close() error
}

// SupernodeClientFactory dials a target supernode and returns a compound-
// proof client. Implementations should reuse the existing supernode-to-
// supernode secure gRPC dialer (see service.go::callGetSliceProof for the
// reference implementation).
type SupernodeClientFactory interface {
	Dial(ctx context.Context, targetSupernodeAccount string) (SupernodeCompoundClient, error)
}

// CascadeMetaProvider returns the cascade metadata for a ticket. The
// resolver in pkg/storagechallenge/lep6_resolution.go consumes the result
// to derive (artifact_count, artifact_key) without round-tripping to the
// chain on the hot path.
type CascadeMetaProvider interface {
	GetCascadeMetadata(ctx context.Context, ticketID string) (*actiontypes.CascadeMetadata, uint64, error)
}

// TicketProvider enumerates the cascade tickets that the given target
// supernode is a participant on. Returns the action_id and the action's
// register-time block height (for ClassifyTicketBucket).
type TicketProvider interface {
	TicketsForTarget(ctx context.Context, targetSupernodeAccount string) ([]TicketDescriptor, error)
}

// TicketDescriptor is a minimal projection of a cascade action that the
// dispatcher needs for bucket classification.
type TicketDescriptor struct {
	TicketID    string
	AnchorBlock int64
}

// NoTicketProvider always reports zero tickets. It is used by tests and as a
// defensive fallback only; production startup wires ChainTicketProvider.
type NoTicketProvider struct{}

// TicketsForTarget always returns nil, nil.
func (NoTicketProvider) TicketsForTarget(_ context.Context, _ string) ([]TicketDescriptor, error) {
	return nil, nil
}

// LEP6Dispatcher is the per-epoch challenger loop. Construct via
// NewLEP6Dispatcher and invoke DispatchEpoch from the storage_challenge
// Service tick.
type LEP6Dispatcher struct {
	client          lumera.Client
	keyring         keyring.Keyring
	keyName         string
	self            string
	supernodeClient SupernodeClientFactory
	tickets         TicketProvider
	meta            CascadeMetaProvider
	buffer          *Buffer
	mu              sync.Mutex
}

// NewLEP6Dispatcher constructs a dispatcher. supernodeClient, tickets,
// meta, and buffer are required; passing nil for any of them returns an
// error.
func NewLEP6Dispatcher(
	client lumera.Client,
	kr keyring.Keyring,
	keyName, self string,
	supernodeClient SupernodeClientFactory,
	tickets TicketProvider,
	meta CascadeMetaProvider,
	buffer *Buffer,
) (*LEP6Dispatcher, error) {
	if client == nil || client.Audit() == nil {
		return nil, fmt.Errorf("lep6 dispatcher: lumera client missing audit module")
	}
	if kr == nil {
		return nil, fmt.Errorf("lep6 dispatcher: keyring is nil")
	}
	if strings.TrimSpace(keyName) == "" {
		return nil, fmt.Errorf("lep6 dispatcher: key name is empty")
	}
	if strings.TrimSpace(self) == "" {
		return nil, fmt.Errorf("lep6 dispatcher: self identity is empty")
	}
	if supernodeClient == nil {
		return nil, fmt.Errorf("lep6 dispatcher: supernode client factory is nil")
	}
	if tickets == nil {
		tickets = NoTicketProvider{}
	}
	if meta == nil {
		return nil, fmt.Errorf("lep6 dispatcher: cascade meta provider is nil")
	}
	if buffer == nil {
		return nil, fmt.Errorf("lep6 dispatcher: result buffer is nil")
	}
	return &LEP6Dispatcher{
		client:          client,
		keyring:         kr,
		keyName:         keyName,
		self:            self,
		supernodeClient: supernodeClient,
		tickets:         tickets,
		meta:            meta,
		buffer:          buffer,
	}, nil
}

// DispatchEpoch runs the challenger flow for epochID. The flow gates on
// StorageTruthEnforcementMode: UNSPECIFIED skips dispatch entirely;
// SHADOW/SOFT/FULL all execute the same off-chain path (chain enforces
// mode-specific side-effects).
//
// Returns nil if the dispatch was skipped (no error), and any error that
// prevents the loop from running at all (e.g., chain queries fail).
// Per-target failures are surfaced as StorageProofResult{ResultClass=FAIL}
// rather than returning an error.
func (d *LEP6Dispatcher) DispatchEpoch(ctx context.Context, epochID uint64) error {
	started := time.Now()
	defer func() { lep6metrics.ObserveDispatchEpochDuration("challenger", time.Since(started)) }()

	paramsResp, err := d.client.Audit().GetParams(ctx)
	if err != nil {
		return fmt.Errorf("lep6 dispatch: get params: %w", err)
	}
	if paramsResp == nil {
		return fmt.Errorf("lep6 dispatch: get params returned nil response")
	}
	params := paramsResp.Params
	mode := params.StorageTruthEnforcementMode

	if mode == audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED {
		logtrace.Debug(ctx, "lep6 dispatch: enforcement mode UNSPECIFIED; skipping", logtrace.Fields{
			"epoch_id": epochID,
		})
		return nil
	}

	anchorResp, err := d.client.Audit().GetEpochAnchor(ctx, epochID)
	if err != nil || anchorResp == nil || anchorResp.Anchor.EpochId != epochID {
		return fmt.Errorf("lep6 dispatch: epoch anchor not yet available for epoch %d", epochID)
	}
	anchor := anchorResp.Anchor

	assigned, err := d.client.Audit().GetAssignedTargets(ctx, d.self, epochID)
	if err != nil || assigned == nil {
		return fmt.Errorf("lep6 dispatch: get assigned targets: %w", err)
	}
	targets := assigned.TargetSupernodeAccounts
	if len(targets) == 0 {
		logtrace.Debug(ctx, "lep6 dispatch: no targets assigned this epoch", logtrace.Fields{
			"epoch_id": epochID,
			"mode":     mode.String(),
		})
		return nil
	}

	// Best-effort current height for bucket classification; if it fails
	// we still run, falling through to UNSPECIFIED bucket = no eligible.
	currentHeight := int64(anchor.EpochEndHeight)
	if currentHeight == 0 {
		if blk, blkErr := d.client.Node().GetLatestBlock(ctx); blkErr == nil && blk != nil {
			if sdk := blk.GetSdkBlock(); sdk != nil {
				currentHeight = sdk.Header.Height
			} else if b := blk.GetBlock(); b != nil {
				currentHeight = b.Header.Height
			}
		}
	}

	logtrace.Info(ctx, "lep6 dispatch: starting epoch", logtrace.Fields{
		"epoch_id": epochID,
		"mode":     mode.String(),
		"targets":  len(targets),
	})

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" || target == d.self {
			continue
		}
		if err := d.dispatchTarget(ctx, epochID, anchor, params, currentHeight, target); err != nil {
			logtrace.Warn(ctx, "lep6 dispatch: target loop error", logtrace.Fields{
				"epoch_id": epochID,
				"target":   target,
				"error":    err.Error(),
			})
		}
	}
	return nil
}

func (d *LEP6Dispatcher) dispatchTarget(
	ctx context.Context,
	epochID uint64,
	anchor audittypes.EpochAnchor,
	params audittypes.Params,
	currentHeight int64,
	target string,
) error {
	tickets, err := d.tickets.TicketsForTarget(ctx, target)
	if err != nil {
		// Treat as transient; emit no-eligible for both buckets so the
		// chain still sees this epoch covered.
		lep6metrics.SetNoTicketProviderActive(true)
		logtrace.Warn(ctx, "lep6 dispatch: ticket provider error", logtrace.Fields{
			"epoch_id": epochID, "target": target, "error": err.Error(),
		})
		tickets = nil
	}

	for _, bucket := range []audittypes.StorageProofBucketType{
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT,
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD,
	} {
		eligibleIDs := make([]string, 0, len(tickets))
		for _, t := range tickets {
			cls := deterministic.ClassifyTicketBucket(currentHeight, t.AnchorBlock,
				params.StorageTruthRecentBucketMaxBlocks, params.StorageTruthOldBucketMinBlocks)
			if cls == bucket {
				eligibleIDs = append(eligibleIDs, t.TicketID)
			}
		}

		if len(eligibleIDs) == 0 {
			lep6metrics.SetNoTicketProviderActive(true)
			d.appendNoEligible(ctx, epochID, anchor, target, bucket)
			continue
		}

		ticketID := deterministic.SelectTicketForBucket(eligibleIDs, nil, anchor.Seed, target, bucket)
		if ticketID == "" {
			lep6metrics.SetNoTicketProviderActive(true)
			d.appendNoEligible(ctx, epochID, anchor, target, bucket)
			continue
		}

		if err := d.dispatchTicket(ctx, epochID, anchor, params, target, bucket, ticketID); err != nil {
			logtrace.Warn(ctx, "lep6 dispatch: ticket loop error", logtrace.Fields{
				"epoch_id": epochID, "target": target, "ticket": ticketID, "error": err.Error(),
			})
		}
	}
	return nil
}

func (d *LEP6Dispatcher) appendNoEligible(
	ctx context.Context,
	epochID uint64,
	anchor audittypes.EpochAnchor,
	target string,
	bucket audittypes.StorageProofBucketType,
) {
	transcriptHashHex, err := deterministic.TranscriptHash(deterministic.TranscriptInputs{
		EpochID:                    epochID,
		ChallengerSupernodeAccount: d.self,
		TargetSupernodeAccount:     target,
		TicketID:                   "",
		Bucket:                     bucket,
		ArtifactClass:              audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED,
	})
	if err != nil {
		logtrace.Warn(ctx, "lep6 dispatch: no-eligible transcript hash error", logtrace.Fields{
			"epoch_id": epochID, "target": target, "error": err.Error(),
		})
		return
	}
	sig, _ := snkeyring.SignBytes(d.keyring, d.keyName, []byte(transcriptHashHex))

	lep6metrics.IncDispatchResult(audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET.String())
	d.buffer.Append(epochID, &audittypes.StorageProofResult{
		TargetSupernodeAccount:     target,
		ChallengerSupernodeAccount: d.self,
		BucketType:                 bucket,
		ResultClass:                audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET,
		TranscriptHash:             transcriptHashHex,
		ChallengerSignature:        hex.EncodeToString(sig),
		Details:                    "no eligible ticket for bucket",
	})
	_ = anchor
}

func (d *LEP6Dispatcher) dispatchTicket(
	ctx context.Context,
	epochID uint64,
	anchor audittypes.EpochAnchor,
	params audittypes.Params,
	target string,
	bucket audittypes.StorageProofBucketType,
	ticketID string,
) error {
	meta, fileSizeKbs, err := d.meta.GetCascadeMetadata(ctx, ticketID)
	if err != nil || meta == nil {
		return fmt.Errorf("get cascade meta: %w", err)
	}

	indexCount, _ := storagechallenge.ResolveArtifactCount(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX)
	symbolCount, _ := storagechallenge.ResolveArtifactCount(meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL)

	class := deterministic.SelectArtifactClass(anchor.Seed, target, ticketID, indexCount, symbolCount)
	if class == audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED {
		d.appendNoEligible(ctx, epochID, anchor, target, bucket)
		return nil
	}

	var artifactCount uint32
	switch class {
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:
		artifactCount = indexCount
	case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:
		artifactCount = symbolCount
	}
	ordinal, err := deterministic.SelectArtifactOrdinal(anchor.Seed, target, ticketID, class, artifactCount)
	if err != nil {
		return fmt.Errorf("select ordinal: %w", err)
	}
	artifactKey, err := storagechallenge.ResolveArtifactKey(meta, class, ordinal)
	if err != nil {
		return fmt.Errorf("resolve artifact key: %w", err)
	}
	artifactSize, err := storagechallenge.ResolveArtifactSize(&actiontypes.Action{FileSizeKbs: int64(fileSizeKbs)}, meta, class, ordinal)
	if err != nil {
		return fmt.Errorf("resolve artifact size: %w", err)
	}

	rangeLen := uint64(params.StorageTruthCompoundRangeLenBytes)
	if rangeLen == 0 {
		rangeLen = uint64(deterministic.LEP6CompoundRangeLenBytes)
	}
	k := int(params.StorageTruthCompoundRangesPerArtifact)
	if k == 0 {
		k = deterministic.LEP6CompoundRangesPerArtifact
	}

	offsets, err := deterministic.ComputeMultiRangeOffsets(anchor.Seed, target, ticketID, class, ordinal, artifactSize, rangeLen, k)
	if err != nil {
		return fmt.Errorf("compute offsets: %w", err)
	}
	ranges := make([]*supernode.ByteRange, len(offsets))
	for i, off := range offsets {
		ranges[i] = &supernode.ByteRange{Start: off, End: off + rangeLen}
	}

	derivHash, err := deterministic.DerivationInputHash(anchor.Seed, target, ticketID, class, ordinal, offsets, rangeLen)
	if err != nil {
		return fmt.Errorf("derivation input hash: %w", err)
	}

	challengeID := deriveCompoundChallengeID(anchor.Seed, epochID, target, ticketID, class, ordinal)

	req := &supernode.GetCompoundProofRequest{
		ChallengeId:            challengeID,
		EpochId:                epochID,
		Seed:                   anchor.Seed,
		TicketId:               ticketID,
		TargetSupernodeAccount: target,
		ChallengerAccount:      d.self,
		ArtifactClass:          uint32(class),
		ArtifactOrdinal:        ordinal,
		ArtifactCount:          artifactCount,
		BucketType:             uint32(bucket),
		ArtifactKey:            artifactKey,
		ArtifactSize:           artifactSize,
		Ranges:                 ranges,
	}

	conn, err := d.supernodeClient.Dial(ctx, target)
	if err != nil {
		d.appendFail(ctx, epochID, target, bucket, ticketID, class, ordinal, artifactCount, artifactKey, derivHash, classifyProofFailure(err, "dial"), fmt.Sprintf("dial: %v", err))
		return nil
	}
	defer func() { _ = conn.Close() }()

	resp, err := conn.GetCompoundProof(ctx, req)
	if err != nil || resp == nil || !resp.Ok {
		reason := "no response"
		if err != nil {
			reason = err.Error()
		} else if resp != nil && resp.Error != "" {
			reason = resp.Error
		}
		d.appendFail(ctx, epochID, target, bucket, ticketID, class, ordinal, artifactCount, artifactKey, derivHash, classifyProofFailure(err, reason), reason)
		return nil
	}

	// Local validation: range count + per-range size, and proof hash recompute.
	if len(resp.RangeBytes) != k {
		d.appendFail(ctx, epochID, target, bucket, ticketID, class, ordinal, artifactCount, artifactKey, derivHash, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT, fmt.Sprintf("range count mismatch: got %d want %d", len(resp.RangeBytes), k))
		return nil
	}
	hasher := blake3.New(32, nil)
	for i, b := range resp.RangeBytes {
		if uint64(len(b)) != rangeLen {
			d.appendFail(ctx, epochID, target, bucket, ticketID, class, ordinal, artifactCount, artifactKey, derivHash, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT, fmt.Sprintf("range[%d] size %d != %d", i, len(b), rangeLen))
			return nil
		}
		_, _ = hasher.Write(b)
	}
	gotHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotHash, resp.ProofHashHex) {
		d.appendFail(ctx, epochID, target, bucket, ticketID, class, ordinal, artifactCount, artifactKey, derivHash, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH, fmt.Sprintf("proof hash mismatch: local=%s remote=%s", gotHash, resp.ProofHashHex))
		return nil
	}

	transcriptHashHex, err := deterministic.TranscriptHash(deterministic.TranscriptInputs{
		EpochID:                    epochID,
		ChallengerSupernodeAccount: d.self,
		TargetSupernodeAccount:     target,
		TicketID:                   ticketID,
		Bucket:                     bucket,
		ArtifactClass:              class,
		ArtifactOrdinal:            ordinal,
		ArtifactKey:                artifactKey,
		DerivationInputHash:        derivHash,
		CompoundProofHashHex:       gotHash,
	})
	if err != nil {
		return fmt.Errorf("transcript hash: %w", err)
	}
	sig, signErr := snkeyring.SignBytes(d.keyring, d.keyName, []byte(transcriptHashHex))
	if signErr != nil {
		return fmt.Errorf("sign transcript: %w", signErr)
	}

	lep6metrics.IncDispatchResult(audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS.String())
	d.buffer.Append(epochID, &audittypes.StorageProofResult{
		TargetSupernodeAccount:     target,
		ChallengerSupernodeAccount: d.self,
		TicketId:                   ticketID,
		BucketType:                 bucket,
		ArtifactClass:              class,
		ArtifactOrdinal:            ordinal,
		ArtifactKey:                artifactKey,
		ArtifactCount:              artifactCount,
		ResultClass:                audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS,
		TranscriptHash:             transcriptHashHex,
		DerivationInputHash:        derivHash,
		ChallengerSignature:        hex.EncodeToString(sig),
	})
	return nil
}

func (d *LEP6Dispatcher) appendFail(
	ctx context.Context,
	epochID uint64,
	target string,
	bucket audittypes.StorageProofBucketType,
	ticketID string,
	class audittypes.StorageProofArtifactClass,
	ordinal uint32,
	artifactCount uint32,
	artifactKey string,
	derivHash string,
	resultClass audittypes.StorageProofResultClass,
	reason string,
) {
	transcriptHashHex, err := deterministic.TranscriptHash(deterministic.TranscriptInputs{
		EpochID:                    epochID,
		ChallengerSupernodeAccount: d.self,
		TargetSupernodeAccount:     target,
		TicketID:                   ticketID,
		Bucket:                     bucket,
		ArtifactClass:              class,
		ArtifactOrdinal:            ordinal,
		ArtifactKey:                artifactKey,
		DerivationInputHash:        derivHash,
		// CompoundProofHashHex empty on failure — captures the non-pass shape.
	})
	if err != nil {
		logtrace.Warn(ctx, "lep6 dispatch: fail transcript hash error", logtrace.Fields{
			"epoch_id": epochID, "target": target, "ticket": ticketID, "error": err.Error(),
		})
		return
	}
	sig, _ := snkeyring.SignBytes(d.keyring, d.keyName, []byte(transcriptHashHex))

	lep6metrics.IncDispatchResult(resultClass.String())
	d.buffer.Append(epochID, &audittypes.StorageProofResult{
		TargetSupernodeAccount:     target,
		ChallengerSupernodeAccount: d.self,
		TicketId:                   ticketID,
		BucketType:                 bucket,
		ArtifactClass:              class,
		ArtifactOrdinal:            ordinal,
		ArtifactKey:                artifactKey,
		ArtifactCount:              artifactCount,
		ResultClass:                resultClass,
		TranscriptHash:             transcriptHashHex,
		DerivationInputHash:        derivHash,
		ChallengerSignature:        hex.EncodeToString(sig),
		Details:                    reason,
	})
}

func deriveCompoundChallengeID(seed []byte, epochID uint64, target, ticketID string, class audittypes.StorageProofArtifactClass, ordinal uint32) string {
	h := blake3.New(32, nil)
	_, _ = h.Write(seed)
	_, _ = h.Write([]byte(fmt.Sprintf("lep6:%d:%s:%s:%d:%d", epochID, target, ticketID, int32(class), ordinal)))
	return hex.EncodeToString(h.Sum(nil))
}

func classifyProofFailure(err error, reason string) audittypes.StorageProofResultClass {
	if err == nil {
		lower := strings.ToLower(strings.TrimSpace(reason))
		if lower == "" || strings.Contains(lower, "timeout") || strings.Contains(lower, "no response") {
			return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE
		}
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable:
			return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE
		}
	}
	return audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT
}
