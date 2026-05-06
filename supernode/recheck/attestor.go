package recheck

import (
	"context"
	"errors"
	"fmt"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/chainerrors"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

type TxSubmitter interface {
	SubmitStorageRecheckEvidence(ctx context.Context, epochID uint64, challengedSupernodeAccount, ticketID, challengedResultTranscriptHash, recheckTranscriptHash string, recheckResultClass audittypes.StorageProofResultClass, details string) (*sdktx.BroadcastTxResponse, error)
}

type Attestor struct {
	self  string
	msg   TxSubmitter
	store Store
}

func NewAttestor(self string, msg TxSubmitter, store Store) *Attestor {
	return &Attestor{self: strings.TrimSpace(self), msg: msg, store: store}
}

func (a *Attestor) Submit(ctx context.Context, c Candidate, r RecheckResult) error {
	if a == nil || a.msg == nil || a.store == nil {
		return fmt.Errorf("recheck attestor missing deps")
	}
	if !c.Valid() || c.TargetAccount == a.self || c.OriginalReporter == a.self {
		return fmt.Errorf("invalid recheck candidate")
	}
	if strings.TrimSpace(r.TranscriptHash) == "" || !validRecheckResultClass(r.ResultClass) {
		return fmt.Errorf("invalid recheck result")
	}
	if err := a.store.RecordPendingRecheckSubmission(ctx, c.EpochID, c.TicketID, c.TargetAccount, c.ChallengedTranscriptHash, r.TranscriptHash, r.ResultClass); err != nil {
		// L3 fix: a duplicate (epoch, ticket, target) row is now a typed
		// signal — treat as already-attempted-this-tick and skip.
		if errors.Is(err, queries.ErrLEP6RecheckAlreadyRecorded) {
			lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "stage_dedup")
			return nil
		}
		lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "stage_error")
		return fmt.Errorf("stage recheck evidence before submit: %w", err)
	}
	_, err := a.msg.SubmitStorageRecheckEvidence(ctx, c.EpochID, c.TargetAccount, c.TicketID, c.ChallengedTranscriptHash, r.TranscriptHash, r.ResultClass, r.Details)
	if err != nil {
		// Transient gRPC failures MUST NOT delete the pending row — Wave
		// 0 fix. The next tick retries and chain dedup absorbs duplicates.
		if chainerrors.IsTransientGrpc(err) {
			lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "submit_transient")
			return fmt.Errorf("submit recheck evidence (transient, will retry): %w", err)
		}
		if chainerrors.IsRecheckEvidenceAlreadySubmitted(err) {
			lep6metrics.IncRecheckAlreadySubmitted()
			lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "already_submitted")
			return a.store.MarkRecheckSubmissionSubmitted(ctx, c.EpochID, c.TicketID, c.TargetAccount)
		}
		_ = a.store.DeletePendingRecheckSubmission(ctx, c.EpochID, c.TicketID, c.TargetAccount)
		lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "submit_error")
		return err
	}
	if err := a.store.MarkRecheckSubmissionSubmitted(ctx, c.EpochID, c.TicketID, c.TargetAccount); err != nil {
		lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "mark_error")
		return err
	}
	lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "submitted")
	return nil
}

func validRecheckResultClass(cls audittypes.StorageProofResultClass) bool {
	switch cls {
	case audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_OBSERVER_QUORUM_FAIL,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT:
		return true
	default:
		return false
	}
}

// (Wave 0): isAlreadySubmittedError helper removed; classification is
// centralised in pkg/lumera/chainerrors.IsRecheckEvidenceAlreadySubmitted
// (anchored on the discriminating "recheck evidence already submitted"
// phrase since audittypes.ErrInvalidRecheckEvidence is a generic envelope
// for many distinct rejections), with IsTransientGrpc short-circuit at
// the call site to preserve the pending row across transient failures.
