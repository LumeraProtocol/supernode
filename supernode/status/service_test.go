package status

import (
	"testing"

	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
)

func TestNewSupernodeStatusService_NilConfigStoragePathsEmpty(t *testing.T) {
	svc := NewSupernodeStatusService(nil, nil, nil, nil)
	if len(svc.storagePaths) != 0 {
		t.Fatalf("expected empty storagePaths for nil config, got %#v", svc.storagePaths)
	}
}

func TestNewSupernodeStatusService_StoragePathsUsesBaseDir(t *testing.T) {
	cfg := &config.Config{BaseDir: "/opt/lumera/.supernode"}
	svc := NewSupernodeStatusService(nil, nil, cfg, nil)
	if len(svc.storagePaths) != 1 || svc.storagePaths[0] != cfg.BaseDir {
		t.Fatalf("unexpected storagePaths: %#v", svc.storagePaths)
	}
}

func TestStatusResponse_ExposesLEP6MetricsSnapshot(t *testing.T) {
	lep6metrics.Reset()
	lep6metrics.IncDispatchResult("PASS")
	lep6metrics.IncDispatchResultDetail("PASS", "RECENT", "SYMBOL")
	lep6metrics.IncDispatchInternalFailure("resolve_key")
	lep6metrics.IncDispatchSignFailure("PASS")
	lep6metrics.IncObserverProof("attested")
	lep6metrics.RecordChallenge(lep6metrics.ChallengeSnapshot{
		ChallengeID:           "challenge-1",
		EpochID:               9,
		Challenger:            "lumera1challenger",
		Target:                "lumera1target",
		TicketID:              "ticket-1",
		Bucket:                "RECENT",
		ArtifactClass:         "SYMBOL",
		ArtifactOrdinal:       2,
		ArtifactCount:         3,
		ArtifactKey:           "rqid-1",
		ResultClass:           "PASS",
		TranscriptHash:        "transcript",
		DerivationHash:        "derivation",
		ProofHash:             "proof",
		ObserverCount:         2,
		AttestationCount:      2,
		FailureStage:          "pass",
		CandidateSource:       "holder_set",
		TargetExpectedHolder:  true,
		ObserverCandidates:    []string{"lumera1target", "lumera1observer1", "lumera1observer2"},
		SelectedObservers:     []string{"lumera1observer1", "lumera1observer2"},
		ObserverOutcomes:      []string{"lumera1observer1:attested", "lumera1observer2:attested"},
		EffectiveArtifactSize: 2440,
		RangeLen:              256,
		SizeSource:            "stored",
	})
	lep6metrics.IncHealClaim("submitted")
	lep6metrics.IncHealVerification("submitted", true)
	lep6metrics.IncRecheckSubmission("RECHECK_CONFIRMED_FAIL", "submitted")
	lep6metrics.SetSelfHealingPendingClaims(2)
	t.Cleanup(lep6metrics.Reset)

	svc := NewSupernodeStatusService(nil, nil, nil, nil)
	resp, err := svc.GetStatus(t.Context(), false)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if resp.GetLep6Metrics() == nil {
		t.Fatal("GetStatus() did not include LEP-6 metrics snapshot")
	}
	lep6 := resp.GetLep6Metrics()
	if got := lep6.GetDispatchResultsTotal()["pass"]; got != 1 {
		t.Fatalf("dispatch pass counter = %d, want 1 (all=%#v)", got, lep6.GetDispatchResultsTotal())
	}
	if got := lep6.GetDispatchResultDetailsTotal()["result=pass,bucket=recent,artifact_class=symbol"]; got != 1 {
		t.Fatalf("dispatch result detail counter = %d, want 1 (all=%#v)", got, lep6.GetDispatchResultDetailsTotal())
	}
	if got := lep6.GetDispatchInternalFailuresTotal()["resolve_key"]; got != 1 {
		t.Fatalf("dispatch internal failure counter = %d, want 1", got)
	}
	if got := lep6.GetDispatchSignFailuresTotal()["pass"]; got != 1 {
		t.Fatalf("dispatch sign failure counter = %d, want 1", got)
	}
	if got := lep6.GetObserverProofsTotal()["attested"]; got != 1 {
		t.Fatalf("observer proof counter = %d, want 1", got)
	}
	if len(lep6.GetRecentChallengeRefs()) != 1 {
		t.Fatalf("recent challenge refs len = %d, want 1", len(lep6.GetRecentChallengeRefs()))
	}
	ref := lep6.GetRecentChallengeRefs()[0]
	if ref.GetChallengeId() != "challenge-1" || ref.GetEpochId() != 9 || ref.GetResultClass() != "PASS" {
		t.Fatalf("unexpected recent challenge ref: %#v", ref)
	}
	detail, err := svc.GetLEP6ChallengeDetail(t.Context(), ref.GetChallengeId())
	if err != nil {
		t.Fatalf("GetLEP6ChallengeDetail() error = %v", err)
	}
	if detail.GetTicketId() != "ticket-1" || detail.GetArtifactKey() != "rqid-1" || detail.GetProofHash() != "proof" || detail.GetAttestationCount() != 2 {
		t.Fatalf("unexpected challenge detail: %#v", detail)
	}
	if detail.GetFailureStage() != "pass" || detail.GetCandidateSource() != "holder_set" || !detail.GetTargetExpectedHolder() || detail.GetEffectiveArtifactSize() != 2440 || detail.GetRangeLen() != 256 || detail.GetSizeSource() != "stored" {
		t.Fatalf("missing challenge diagnostics: %#v", detail)
	}
	if len(detail.GetObserverCandidates()) != 3 || len(detail.GetSelectedObservers()) != 2 || len(detail.GetObserverOutcomes()) != 2 {
		t.Fatalf("unexpected challenge observer diagnostics: %#v", detail)
	}
	if _, err := svc.GetLEP6ChallengeDetail(t.Context(), "missing"); err == nil {
		t.Fatal("GetLEP6ChallengeDetail(missing) expected error")
	}
	if got := lep6.GetHealClaimsSubmittedTotal()["submitted"]; got != 1 {
		t.Fatalf("heal claim submitted counter = %d, want 1", got)
	}
	if got := lep6.GetHealVerificationsSubmittedTotal()["verified=positive,result=submitted"]; got != 1 {
		t.Fatalf("heal verification submitted counter = %d, want 1", got)
	}
	if got := lep6.GetRecheckEvidenceSubmittedTotal()["class=recheck_confirmed_fail,outcome=submitted"]; got != 1 {
		t.Fatalf("recheck evidence submitted counter = %d, want 1", got)
	}
	if got := lep6.GetSelfHealingPendingClaims(); got != 2 {
		t.Fatalf("self-healing pending claims = %d, want 2", got)
	}
}
