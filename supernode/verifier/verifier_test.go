package verifier

import (
	"strings"
	"testing"

	snmodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
)

func TestCheckSupernodeStateAllowsStorageFullWithWarning(t *testing.T) {
	cv := &ConfigVerifier{}
	result := &VerificationResult{Valid: true}

	cv.checkSupernodeState(result, &snmodule.SuperNodeInfo{CurrentState: "SUPERNODE_STATE_STORAGE_FULL"})

	if !result.Valid {
		t.Fatalf("expected STORAGE_FULL to remain valid")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %d", len(result.Errors))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(result.Warnings))
	}
	if !strings.Contains(result.Warnings[0].Message, "eligible for storage rewards") {
		t.Fatalf("STORAGE_FULL warning should mention reward eligibility, got: %s", result.Warnings[0].Message)
	}
}

func TestCheckSupernodeStateAllowsPostponedWithWarning(t *testing.T) {
	cv := &ConfigVerifier{}
	result := &VerificationResult{Valid: true}

	cv.checkSupernodeState(result, &snmodule.SuperNodeInfo{CurrentState: "SUPERNODE_STATE_POSTPONED"})

	if !result.Valid {
		t.Fatalf("expected POSTPONED to remain valid")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %d", len(result.Errors))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(result.Warnings))
	}
	if !strings.Contains(result.Warnings[0].Message, "not eligible for storage rewards") {
		t.Fatalf("POSTPONED warning should mention ineligibility for rewards, got: %s", result.Warnings[0].Message)
	}
}
