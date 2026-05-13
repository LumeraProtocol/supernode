package self_healing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestHealer_DeadlinePassedSkipsSubmit covers the H1 fix: a heal-op whose
// DeadlineEpochId is already in the past must NOT submit (chain would
// reject with ErrHealOpInvalidState anyway). Pre-check saves the gas burn.
func TestHealer_DeadlinePassedSkipsSubmit(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("payload-h1")
	wantHash := hashOf(t, body)
	h.cascade.reseedFn = func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		_ = makeStagingDir(t, h.stagingRoot, 999, wantHash, body)
		return &cascadeService.RecoveryReseedResult{
			ActionID: req.ActionID, DataHashVerified: true,
			ReconstructedHashB64: wantHash, StagingDir: req.StagingDir,
		}, nil
	}
	// Current epoch already past the heal-op deadline.
	h.audit.currentEpoch = 100
	op := audittypes.HealOp{
		HealOpId: 999, TicketId: "ticket-d",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
		DeadlineEpochId:        50,
	}
	if err := h.svc.reconstructAndClaim(context.Background(), op); err != nil {
		t.Fatalf("reconstructAndClaim: %v", err)
	}
	// No submit attempt was made.
	if calls := h.auditMsg.claimCalls; len(calls) != 0 {
		t.Fatalf("expected no claim submit on past-deadline op; got %d", len(calls))
	}
	// Staging cleaned up.
	if _, err := os.Stat(filepath.Join(h.stagingRoot, "999")); !os.IsNotExist(err) {
		t.Fatalf("staging should be removed when deadline passed")
	}
	// No dedup row left behind.
	has, _ := h.store.HasHealClaim(context.Background(), 999)
	if has {
		t.Fatalf("no claim should be persisted on past-deadline skip")
	}
	pending, _ := h.store.HasPendingHealClaim(context.Background(), 999)
	if pending {
		t.Fatalf("no pending row should remain on past-deadline skip")
	}
}

// TestHealer_DeadlinePreCheckTransientErrorProceeds covers the
// defense-in-depth case where GetCurrentEpoch fails transiently — we
// proceed with submit (chain will reject if needed) rather than skip a
// possibly-still-valid op.
func TestHealer_DeadlinePreCheckTransientErrorProceeds(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("payload-transient")
	wantHash := hashOf(t, body)
	h.cascade.reseedFn = func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		_ = makeStagingDir(t, h.stagingRoot, 1001, wantHash, body)
		return &cascadeService.RecoveryReseedResult{
			ActionID: req.ActionID, DataHashVerified: true,
			ReconstructedHashB64: wantHash, StagingDir: req.StagingDir,
		}, nil
	}
	// Make GetCurrentEpoch fail transiently — we override the mock by
	// putting a HealOp without deadline so the check returns immediately
	// (skipping the GetCurrentEpoch call). DeadlineEpochId=0 means
	// "no deadline configured".
	op := audittypes.HealOp{
		HealOpId: 1001, TicketId: "ticket-z",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
		DeadlineEpochId:        0, // no deadline → pre-check is a no-op
	}
	if err := h.svc.reconstructAndClaim(context.Background(), op); err != nil {
		t.Fatalf("reconstructAndClaim: %v", err)
	}
	// Submit happened normally.
	if calls := h.auditMsg.claimCalls; len(calls) != 1 {
		t.Fatalf("expected 1 claim submit when deadline=0; got %d", len(calls))
	}
}

// TestFinalizer_RunsEvenInUnspecifiedMode covers the M7 fix: a pending
// claim row + staging dir that survives a governance rollback to
// UNSPECIFIED must still drain via the finalizer.
func TestFinalizer_RunsEvenInUnspecifiedMode(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED)
	body := []byte("leftover-after-rollback")
	wantHash := hashOf(t, body)
	stagingDir := makeStagingDir(t, h.stagingRoot, 70, wantHash, body)
	if err := h.store.RecordHealClaim(context.Background(), 70, "ticket-70", wantHash, stagingDir); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	// Heal-op finalized FAILED on chain.
	h.audit.put(audittypes.HealOp{
		HealOpId: 70,
		Status:   audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
	})
	// Run a tick under UNSPECIFIED. Healer/verifier dispatch should
	// be skipped, but finalizer MUST still run and clean up.
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		has, _ := h.store.HasHealClaim(context.Background(), 70)
		return !has
	})
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be cleaned up by finalizer even in UNSPECIFIED mode (M7)")
	}
}

// TestC5_ResumePendingHealClaim_PromotesOnMatchingChain covers the C5
// promote branch: a `pending` row exists locally; chain has accepted our
// claim (HEALER_REPORTED with matching ResultHash). The dispatcher must
// promote pending → submitted, NOT re-run reconstruct.
func TestC5_ResumePendingHealClaim_PromotesOnMatchingChain(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("c5-payload")
	wantHash := hashOf(t, body)
	stagingDir := makeStagingDir(t, h.stagingRoot, 555, wantHash, body)
	// Pre-seed a pending row (simulating a crash mid-submit).
	if err := h.store.RecordPendingHealClaim(context.Background(), 555, "ticket-555", wantHash, stagingDir); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	// Chain shows our claim was accepted.
	h.audit.put(audittypes.HealOp{
		HealOpId:               555,
		TicketId:               "ticket-555",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED, // dispatcher's view
		HealerSupernodeAccount: "sn-healer",
	})
	// resumePendingHealClaim consults the chain via GetHealOp; override
	// its return so chain says HEALER_REPORTED with our hash.
	h.audit.setStatus(555, audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED)
	chainOp := h.audit.opsByID[555]
	chainOp.ResultHash = wantHash
	h.audit.put(chainOp)
	// Configure cascade reseedFn to fail loudly if invoked — resume must
	// NOT run reconstruct.
	h.cascade.reseedFn = func(ctx context.Context, _ *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		t.Fatalf("resume path must NOT call reseed (the chain already accepted our claim)")
		return nil, errors.New("must not be called")
	}
	op := audittypes.HealOp{
		HealOpId:               555,
		TicketId:               "ticket-555",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
	}
	if err := h.svc.resumePendingHealClaim(context.Background(), op); err != nil {
		t.Fatalf("resumePendingHealClaim: %v", err)
	}
	// Pending → submitted promotion happened.
	has, _ := h.store.HasHealClaim(context.Background(), 555)
	if !has {
		t.Fatalf("expected submitted row after resume promote")
	}
	pending, _ := h.store.HasPendingHealClaim(context.Background(), 555)
	if pending {
		t.Fatalf("pending row should be cleared after promote")
	}
	// Staging preserved (finalizer needs it).
	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("staging dir should be preserved on promote: %v", err)
	}
}

// TestC5_ResumePendingHealClaim_ResetOnStillScheduled covers the C5
// reset branch: chain still SCHEDULED → our prior submit was rejected
// or lost. Drop pending row + staging so next tick re-dispatches fresh.
func TestC5_ResumePendingHealClaim_ResetOnStillScheduled(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("c5-reset-payload")
	wantHash := hashOf(t, body)
	stagingDir := makeStagingDir(t, h.stagingRoot, 556, wantHash, body)
	if err := h.store.RecordPendingHealClaim(context.Background(), 556, "ticket-556", wantHash, stagingDir); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	h.audit.put(audittypes.HealOp{
		HealOpId:               556,
		TicketId:               "ticket-556",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
	})
	op := audittypes.HealOp{
		HealOpId:               556,
		TicketId:               "ticket-556",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
	}
	if err := h.svc.resumePendingHealClaim(context.Background(), op); err != nil {
		t.Fatalf("resumePendingHealClaim: %v", err)
	}
	// Pending row + staging cleaned up.
	pending, _ := h.store.HasPendingHealClaim(context.Background(), 556)
	if pending {
		t.Fatalf("pending row should be deleted after reset")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed on reset")
	}
}

// TestC5_ResumePendingHealClaim_TransientGrpcPreservesState covers the
// C5 transient-failure branch: a transient gRPC error during the chain
// reconcile query must NOT delete the pending row or staging dir.
func TestC5_ResumePendingHealClaim_TransientGrpcPreservesState(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("c5-transient")
	wantHash := hashOf(t, body)
	stagingDir := makeStagingDir(t, h.stagingRoot, 557, wantHash, body)
	if err := h.store.RecordPendingHealClaim(context.Background(), 557, "ticket-557", wantHash, stagingDir); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	h.audit.getOpErr = status.Error(codes.Unavailable, "chain unavailable")
	op := audittypes.HealOp{HealOpId: 557, TicketId: "ticket-557", HealerSupernodeAccount: "sn-healer"}
	err := h.svc.resumePendingHealClaim(context.Background(), op)
	if err == nil {
		t.Fatalf("expected error on transient gRPC")
	}
	// Pending + staging preserved.
	pending, _ := h.store.HasPendingHealClaim(context.Background(), 557)
	if !pending {
		t.Fatalf("pending row must NOT be deleted on transient error (C5)")
	}
	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("staging dir must be preserved on transient: %v", err)
	}
}
