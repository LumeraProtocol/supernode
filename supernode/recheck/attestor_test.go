package recheck

import (
	"context"
	"errors"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func TestAttestor_SubmitsThenPersists(t *testing.T) {
	callSeq = 0
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{}
	a := NewAttestor("self", msg, store)

	candidate := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS, Details: "ok"}

	require.NoError(t, a.Submit(ctx, candidate, result))
	require.Len(t, msg.calls, 1)
	require.Equal(t, 1, msg.calls[0].callIndex)
	require.Greater(t, store.recordCallIndex, msg.calls[0].callIndex)
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.True(t, exists)
}

func TestAttestor_DoesNotPersistOnTxFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{err: errBoom}
	a := NewAttestor("self", msg, store)

	candidate := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS}

	require.Error(t, a.Submit(ctx, candidate, result))
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestAttestor_AcceptsExistingChainRecheckAsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{err: errAlreadyOnChain}
	a := NewAttestor("self", msg, store)

	candidate := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL}

	require.NoError(t, a.Submit(ctx, candidate, result))
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.True(t, exists)
}

func TestAttestor_DoesNotTreatGenericDuplicateWordsAsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{err: errors.New("connection already closed before duplicate retry could be replayed")}
	a := NewAttestor("self", msg, store)

	candidate := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL}

	require.Error(t, a.Submit(ctx, candidate, result))
	exists, err := store.HasRecheckSubmission(ctx, 7, "ticket-1")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestAttestor_RejectsSelfReportedOrSelfTargetCandidate(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{}
	attestor := NewAttestor("self", msg, store)
	base := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "recheck-hash", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS}

	selfReporter := base
	selfReporter.OriginalReporter = "self"
	require.Error(t, attestor.Submit(ctx, selfReporter, result))

	selfTarget := base
	selfTarget.TargetAccount = "self"
	require.Error(t, attestor.Submit(ctx, selfTarget, result))
	require.Empty(t, msg.calls)
}

func TestAttestor_RejectsEmptyRequiredFieldsBeforeTx(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	msg := &recordingAuditMsg{}
	a := NewAttestor("self", msg, store)

	candidate := Candidate{EpochID: 7, TargetAccount: "target", TicketID: "ticket-1", ChallengedTranscriptHash: "orig-hash", OriginalReporter: "reporter", OriginalResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH}
	result := RecheckResult{TranscriptHash: "", ResultClass: audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS}

	require.Error(t, a.Submit(ctx, candidate, result))
	require.Empty(t, msg.calls)
}
