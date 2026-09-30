package lep6

import (
	"testing"
	"time"
)

func TestSnapshotTracksFullLEP6SignalSet(t *testing.T) {
	Reset()

	IncDispatchResult("PASS")
	IncDispatchResultDetail("PASS", "OLD", "SYMBOL")
	IncDispatchInternalFailure("resolve_size")
	IncDispatchSignFailure("PASS")
	IncObserverProof("attested")
	RecordChallenge(ChallengeSnapshot{
		ChallengeID:      "challenge-1",
		EpochID:          9,
		Challenger:       "lumera1challenger",
		Target:           "lumera1target",
		TicketID:         "ticket-1",
		Bucket:           "RECENT",
		ArtifactClass:    "SYMBOL",
		ArtifactOrdinal:  2,
		ArtifactCount:    3,
		ArtifactKey:      "rqid-1",
		ResultClass:      "PASS",
		TranscriptHash:   "transcript",
		DerivationHash:   "derivation",
		ProofHash:        "proof",
		ObserverCount:    2,
		AttestationCount: 2,
	})
	IncDispatchThrottled("drop-non-RECENT-first", 3)
	ObserveDispatchEpochDuration("challenger", 1500*time.Millisecond)
	ObserveDispatchEpochDuration("challenger", 500*time.Millisecond)
	IncTicketDiscovery("eligible")
	SetNoTicketProviderActive(true)

	IncHealClaim("submitted")
	IncHealClaimReconciled()
	IncHealVerification("submitted", true)
	IncHealVerification("dedup", false)
	IncHealVerificationAlreadyExists()
	IncHealFinalizePublish()
	IncHealFinalizeCleanup("FAILED")
	SetSelfHealingPendingClaims(2)
	SetSelfHealingStagingBytes(4096)

	IncRecheckCandidateFound()
	IncRecheckSubmission("RECHECK_CONFIRMED_FAIL", "submitted")
	IncRecheckAlreadySubmitted()
	IncRecheckFailure("execute")
	SetRecheckPendingCandidates(7)

	s := Snapshot()
	assertCounter(t, s.DispatchResultsTotal, "pass", 1)
	assertCounter(t, s.DispatchResultDetailsTotal, "result=pass,bucket=old,artifact_class=symbol", 1)
	assertCounter(t, s.DispatchInternalFailuresTotal, "resolve_size", 1)
	assertCounter(t, s.DispatchSignFailuresTotal, "pass", 1)
	assertCounter(t, s.ObserverProofsTotal, "attested", 1)
	if len(s.RecentChallengeRefs) != 1 {
		t.Fatalf("RecentChallengeRefs len = %d, want 1", len(s.RecentChallengeRefs))
	}
	ref := s.RecentChallengeRefs[0]
	if ref.ChallengeID != "challenge-1" || ref.TimestampUnix == 0 || ref.EpochID != 9 || ref.ResultClass != "PASS" {
		t.Fatalf("unexpected challenge ref: %#v", ref)
	}
	challenge, ok := GetChallenge("challenge-1")
	if !ok {
		t.Fatal("GetChallenge did not find recorded challenge")
	}
	if challenge.TicketID != "ticket-1" || challenge.ArtifactKey != "rqid-1" || challenge.ProofHash != "proof" || challenge.AttestationCount != 2 {
		t.Fatalf("unexpected challenge detail: %#v", challenge)
	}
	assertCounter(t, s.DispatchThrottledTotal, "drop-non-recent-first", 3)
	assertCounter(t, s.DispatchEpochDurationMillisTotal, "challenger", 2000)
	assertCounter(t, s.DispatchEpochDurationMillisMax, "challenger", 1500)
	assertCounter(t, s.DispatchEpochDurationCount, "challenger", 2)
	assertCounter(t, s.TicketDiscoveryTotal, "eligible", 1)
	if s.NoTicketProviderActive != 1 {
		t.Fatalf("NoTicketProviderActive = %d, want 1", s.NoTicketProviderActive)
	}
	assertCounter(t, s.HealClaimsSubmittedTotal, "submitted", 1)
	if s.HealClaimsReconciledTotal != 1 {
		t.Fatalf("HealClaimsReconciledTotal = %d, want 1", s.HealClaimsReconciledTotal)
	}
	assertCounter(t, s.HealVerificationsSubmittedTotal, "verified=positive,result=submitted", 1)
	assertCounter(t, s.HealVerificationsSubmittedTotal, "verified=negative,result=dedup", 1)
	if s.HealVerificationsAlreadyExistsTotal != 1 {
		t.Fatalf("HealVerificationsAlreadyExistsTotal = %d, want 1", s.HealVerificationsAlreadyExistsTotal)
	}
	if s.HealFinalizePublishesTotal != 1 {
		t.Fatalf("HealFinalizePublishesTotal = %d, want 1", s.HealFinalizePublishesTotal)
	}
	assertCounter(t, s.HealFinalizeCleanupsTotal, "failed", 1)
	if s.SelfHealingPendingClaims != 2 || s.SelfHealingStagingBytes != 4096 {
		t.Fatalf("self-healing gauges = (%d,%d), want (2,4096)", s.SelfHealingPendingClaims, s.SelfHealingStagingBytes)
	}
	if s.RecheckCandidatesFoundTotal != 1 {
		t.Fatalf("RecheckCandidatesFoundTotal = %d, want 1", s.RecheckCandidatesFoundTotal)
	}
	assertCounter(t, s.RecheckEvidenceSubmittedTotal, "class=recheck_confirmed_fail,outcome=submitted", 1)
	if s.RecheckEvidenceAlreadySubmittedTotal != 1 {
		t.Fatalf("RecheckEvidenceAlreadySubmittedTotal = %d, want 1", s.RecheckEvidenceAlreadySubmittedTotal)
	}
	assertCounter(t, s.RecheckExecutionFailuresTotal, "execute", 1)
	if s.RecheckPendingCandidates != 7 {
		t.Fatalf("RecheckPendingCandidates = %d, want 7", s.RecheckPendingCandidates)
	}
}

func TestResetClearsMetrics(t *testing.T) {
	Reset()
	IncDispatchResult("PASS")
	RecordChallenge(ChallengeSnapshot{ChallengeID: "challenge-reset", EpochID: 1})
	SetSelfHealingPendingClaims(9)
	Reset()
	s := Snapshot()
	if len(s.DispatchResultsTotal) != 0 {
		t.Fatalf("DispatchResultsTotal after Reset = %#v, want empty", s.DispatchResultsTotal)
	}
	if len(s.RecentChallengeRefs) != 0 {
		t.Fatalf("RecentChallengeRefs after Reset = %#v, want empty", s.RecentChallengeRefs)
	}
	if _, ok := GetChallenge("challenge-reset"); ok {
		t.Fatal("GetChallenge found reset challenge")
	}
	if s.SelfHealingPendingClaims != 0 {
		t.Fatalf("SelfHealingPendingClaims after Reset = %d, want 0", s.SelfHealingPendingClaims)
	}
}

func assertCounter(t *testing.T, got map[string]uint64, key string, want uint64) {
	t.Helper()
	if got[key] != want {
		t.Fatalf("counter[%q] = %d, want %d (all=%#v)", key, got[key], want, got)
	}
}
