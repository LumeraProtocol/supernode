package verifier

import (
	"testing"

	snmodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
)

func TestCheckSupernodeState_AllowsStorageFull(t *testing.T) {
	cv := &ConfigVerifier{}
	result := &VerificationResult{Valid: true}

	cv.checkSupernodeState(result, &snmodule.SuperNodeInfo{CurrentState: "SUPERNODE_STATE_STORAGE_FULL"})

	if !result.Valid {
		t.Fatalf("expected STORAGE_FULL to be allowed, got invalid result: %+v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got: %+v", result.Errors)
	}
}

func TestCheckSupernodeState_PostponedWarnsNotErrors(t *testing.T) {
	cv := &ConfigVerifier{}
	result := &VerificationResult{Valid: true}

	cv.checkSupernodeState(result, &snmodule.SuperNodeInfo{CurrentState: "SUPERNODE_STATE_POSTPONED"})

	if !result.Valid {
		t.Fatalf("expected POSTPONED to remain valid with warning")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got: %+v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warning for POSTPONED state")
	}
}
