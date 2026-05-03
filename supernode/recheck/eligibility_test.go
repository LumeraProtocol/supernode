package recheck

import (
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func TestRecheckEligible_AcceptsChainEligibleFailureClasses(t *testing.T) {
	for _, cls := range []audittypes.StorageProofResultClass{
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_OBSERVER_QUORUM_FAIL,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT,
	} {
		require.True(t, IsRecheckEligibleResultClass(cls), cls.String())
	}
}

func TestRecheckEligible_RejectsPassAndRecheckConfirmedFail(t *testing.T) {
	for _, cls := range []audittypes.StorageProofResultClass{
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_UNSPECIFIED,
	} {
		require.False(t, IsRecheckEligibleResultClass(cls), cls.String())
	}
}

func TestMapRecheckOutcome_PreservesSpecFidelity(t *testing.T) {
	require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS, MapRecheckOutcome(OutcomePass))
	require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL, MapRecheckOutcome(OutcomeConfirmedHashMismatch))
	require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE, MapRecheckOutcome(OutcomeTimeout))
	require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_OBSERVER_QUORUM_FAIL, MapRecheckOutcome(OutcomeObserverQuorumFail))
	require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT, MapRecheckOutcome(OutcomeInvalidTranscript))
}
