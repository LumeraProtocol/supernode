package recheck

import (
	"context"
	"fmt"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
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
		lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "stage_error")
		return fmt.Errorf("stage recheck evidence before submit: %w", err)
	}
	_, err := a.msg.SubmitStorageRecheckEvidence(ctx, c.EpochID, c.TargetAccount, c.TicketID, c.ChallengedTranscriptHash, r.TranscriptHash, r.ResultClass, r.Details)
	if err != nil {
		if isAlreadySubmittedError(err) {
			lep6metrics.IncRecheckAlreadySubmitted()
			lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "already_submitted")
			return a.store.MarkRecheckSubmissionSubmitted(ctx, c.EpochID, c.TicketID)
		}
		_ = a.store.DeletePendingRecheckSubmission(ctx, c.EpochID, c.TicketID)
		lep6metrics.IncRecheckSubmission(r.ResultClass.String(), "submit_error")
		return err
	}
	if err := a.store.MarkRecheckSubmissionSubmitted(ctx, c.EpochID, c.TicketID); err != nil {
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

func isAlreadySubmittedError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "recheck evidence already submitted")
}
