package audit_msg

import (
	"context"
	"strings"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func TestClaimHealCompleteValidatesInputsBeforeTxExecution(t *testing.T) {
	m := &module{}
	_, err := m.ClaimHealComplete(context.Background(), 0, "ticket", "manifest", "")
	require.ErrorContains(t, err, "heal op id cannot be zero")

	_, err = m.ClaimHealComplete(context.Background(), 1, "   ", "manifest", "")
	require.ErrorContains(t, err, "ticket id cannot be empty")

	_, err = m.ClaimHealComplete(context.Background(), 1, "ticket", "   ", "")
	require.ErrorContains(t, err, "heal manifest hash cannot be empty")
}

func TestSubmitHealVerificationValidatesInputsBeforeTxExecution(t *testing.T) {
	m := &module{}
	_, err := m.SubmitHealVerification(context.Background(), 0, true, "hash", "")
	require.ErrorContains(t, err, "heal op id cannot be zero")

	_, err = m.SubmitHealVerification(context.Background(), 1, true, "   ", "")
	require.ErrorContains(t, err, "verification hash cannot be empty")
}

func TestSubmitStorageRecheckEvidenceValidatesInputsBeforeTxExecution(t *testing.T) {
	m := &module{}
	_, err := m.SubmitStorageRecheckEvidence(context.Background(), 7, "   ", "ticket", "challenged", "recheck", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL, "")
	require.ErrorContains(t, err, "challenged supernode account cannot be empty")

	_, err = m.SubmitStorageRecheckEvidence(context.Background(), 7, "target", "   ", "challenged", "recheck", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL, "")
	require.ErrorContains(t, err, "ticket id cannot be empty")

	_, err = m.SubmitStorageRecheckEvidence(context.Background(), 7, "target", "ticket", "   ", "recheck", audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL, "")
	require.ErrorContains(t, err, "challenged result transcript hash cannot be empty")

	_, err = m.SubmitStorageRecheckEvidence(context.Background(), 7, "target", "ticket", "challenged", strings.Repeat(" ", 3), audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL, "")
	require.ErrorContains(t, err, "recheck transcript hash cannot be empty")
}
