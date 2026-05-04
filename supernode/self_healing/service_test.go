package self_healing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

// helper builds a Service + its hooks for testing. Returns Service plus the
// programmable mocks so individual tests can drive scenarios.
type harness struct {
	svc         *Service
	audit       *programmableAudit
	auditMsg    *programmableAuditMsg
	cascade     *fakeCascadeFactory
	store       queries.LocalStoreInterface
	stagingRoot string
	identity    string
}

func newHarness(t *testing.T, identity string, mode audittypes.StorageTruthEnforcementMode) *harness {
	t.Helper()
	a := newProgrammableAudit(mode)
	am := newProgrammableAuditMsg()
	cf := newFakeCascadeFactory()
	store := newTestStore(t)
	root := filepath.Join(t.TempDir(), "heal-staging")
	cfg := Config{
		Enabled:                    true,
		PollInterval:               time.Second,
		MaxConcurrentReconstructs:  2,
		MaxConcurrentVerifications: 4,
		MaxConcurrentPublishes:     2,
		StagingRoot:                root,
		VerifierFetchAttempts:      2,
		VerifierFetchTimeout:       time.Second,
		VerifierBackoffBase:        10 * time.Millisecond,
		KeyName:                    "test",
	}
	svc, err := New(identity, cfg, newFakeLumera(a, am), store, cf, &fakeFetcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{svc: svc, audit: a, auditMsg: am, cascade: cf, store: store, stagingRoot: root, identity: identity}
}

// newTestStore mirrors the test helper in pkg/storage/queries; we re-create
// it here so this package's tests don't depend on internal sqlite test
// scaffolding.
func newTestStore(t *testing.T) queries.LocalStoreInterface {
	// Reuse the public OpenHistoryDB by setting HOME to a tempdir so the
	// resolved ~/.supernode/history.db lives there.
	t.Helper()
	tmp := t.TempDir()
	old := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", old) })
	store, err := queries.OpenHistoryDB()
	if err != nil {
		t.Fatalf("OpenHistoryDB: %v", err)
	}
	t.Cleanup(func() { store.CloseHistoryDB(context.Background()) })
	return store
}

// fakeFetcher returns a configurable response. Configure per-test by
// reassigning .body / .err.
type fakeFetcher struct {
	body []byte
	err  error
}

func (f *fakeFetcher) FetchReconstructed(ctx context.Context, healOpID uint64, healerAccount, verifierAccount string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.body...), nil
}

// hashOf returns the action.DataHash recipe (BLAKE3 base64) of body. Used as
// the expected op.ResultHash in verifier tests.
func hashOf(t *testing.T, body []byte) string {
	t.Helper()
	h, err := cascadekit.ComputeBlake3DataHashB64(body)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// ---------------------------------------------------------------------------
// Test 1 — TestVerifier_ReadsOpResultHashForComparison (R-bug regression).
// ---------------------------------------------------------------------------
//
// Spec: verifier MUST submit verified=true only when its computed hash
// equals op.ResultHash (chain enforcement at msg_storage_truth.go:291).
// The supernode does not read Action.DataHash anywhere in the heal flow,
// so the regression surface is "do we read op.ResultHash and compare
// against THAT?". This test gives the verifier a body whose hash matches
// op.ResultHash and asserts verified=true with VerificationHash equal to
// the computed hash. A regression that hard-coded a constant or pulled
// from a different field would fail this test.
func TestVerifier_ReadsOpResultHashForComparison(t *testing.T) {
	h := newHarness(t, "sn-verifier", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)

	body := []byte("recovered-bytes-OK")
	// The whole point of the R-bug pin: op.ResultHash is what the healer
	// reported; verifier must compare against THIS.
	h.audit.put(audittypes.HealOp{
		HealOpId:                  10,
		TicketId:                  "ticket-x",
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-healer",
		VerifierSupernodeAccounts: []string{"sn-verifier"},
		ResultHash:                hashOf(t, body),
	})
	h.svc.fetcher = &fakeFetcher{body: body}
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForVerifications(t, h.auditMsg, 1)
	_, vc := h.auditMsg.snapshot()
	if len(vc) != 1 {
		t.Fatalf("expected 1 verification call, got %d", len(vc))
	}
	if !vc[0].Verified {
		t.Fatalf("expected verified=true (computed==op.ResultHash); details=%q", vc[0].Details)
	}
	if vc[0].VerificationHash != hashOf(t, body) {
		t.Fatalf("VerificationHash should equal computed hash; got %q want %q", vc[0].VerificationHash, hashOf(t, body))
	}
}

// ---------------------------------------------------------------------------
// Test 2 — TestVerifier_HashMismatchProducesVerifiedFalse.
// ---------------------------------------------------------------------------
func TestVerifier_HashMismatchProducesVerifiedFalse(t *testing.T) {
	h := newHarness(t, "sn-verifier", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	wantBody := []byte("expected-body")
	gotBody := []byte("tampered-body")
	h.audit.put(audittypes.HealOp{
		HealOpId:                  11,
		TicketId:                  "ticket-y",
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-healer",
		VerifierSupernodeAccounts: []string{"sn-verifier"},
		ResultHash:                hashOf(t, wantBody),
	})
	h.svc.fetcher = &fakeFetcher{body: gotBody}
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForVerifications(t, h.auditMsg, 1)
	_, vc := h.auditMsg.snapshot()
	if vc[0].Verified {
		t.Fatalf("expected verified=false on hash mismatch")
	}
	if !strings.Contains(vc[0].Details, "hash_mismatch") {
		t.Fatalf("expected details to mention hash_mismatch, got %q", vc[0].Details)
	}
	if vc[0].VerificationHash == "" {
		t.Fatalf("VerificationHash must be non-empty even on negative votes (chain rejects empty)")
	}
}

// ---------------------------------------------------------------------------
// Test 2b — TestVerifier_FetchFailureSubmitsNonEmptyHash.
// ---------------------------------------------------------------------------
//
// BLOCKER fix regression: chain rejects empty VerificationHash even on
// verified=false (msg_storage_truth.go:271-273). When the verifier can't
// reach the healer, it MUST synthesize a non-empty placeholder hash so the
// negative attestation is well-formed.
func TestVerifier_FetchFailureSubmitsNonEmptyHash(t *testing.T) {
	h := newHarness(t, "sn-verifier", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	h.audit.put(audittypes.HealOp{
		HealOpId:                  13,
		TicketId:                  "ticket-fetch-fail",
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-unreachable-healer",
		VerifierSupernodeAccounts: []string{"sn-verifier"},
		ResultHash:                hashOf(t, []byte("expected")),
	})
	h.svc.fetcher = &fakeFetcher{err: errors.New("connection refused")}
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForVerifications(t, h.auditMsg, 1)
	_, vc := h.auditMsg.snapshot()
	if vc[0].Verified {
		t.Fatalf("expected verified=false on fetch failure")
	}
	if vc[0].VerificationHash == "" {
		t.Fatalf("BLOCKER regression: VerificationHash must be non-empty (chain rejects empty for both positive and negative)")
	}
	if !strings.Contains(vc[0].Details, "fetch_failed") {
		t.Fatalf("details should record reason; got %q", vc[0].Details)
	}
}

// ---------------------------------------------------------------------------
// Test 3 — TestVerifier_FetchesFromAssignedHealerOnly (§19 gate).
// ---------------------------------------------------------------------------
//
// Verifier passes (op.HealerSupernodeAccount, identity) to the fetcher and
// nothing else. Verifier must never address an arbitrary peer or KAD.
func TestVerifier_FetchesFromAssignedHealerOnly(t *testing.T) {
	h := newHarness(t, "sn-verifier", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("payload")
	h.audit.put(audittypes.HealOp{
		HealOpId:                  12,
		TicketId:                  "ticket-z",
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-healer-7",
		VerifierSupernodeAccounts: []string{"sn-verifier", "sn-other"},
		ResultHash:                hashOf(t, body),
	})
	rec := &recordingFetcher{body: body}
	h.svc.fetcher = rec
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForVerifications(t, h.auditMsg, 1)
	if rec.lastHealer != "sn-healer-7" {
		t.Fatalf("verifier addressed wrong healer: got %q want sn-healer-7", rec.lastHealer)
	}
	if rec.lastVerifier != "sn-verifier" {
		t.Fatalf("verifier identity not propagated: got %q", rec.lastVerifier)
	}
	if rec.calls != 1 {
		t.Fatalf("expected exactly 1 fetch call, got %d", rec.calls)
	}
}

type recordingFetcher struct {
	body         []byte
	lastHealer   string
	lastVerifier string
	calls        int
}

func (r *recordingFetcher) FetchReconstructed(ctx context.Context, healOpID uint64, healerAccount, verifierAccount string) ([]byte, error) {
	r.lastHealer = healerAccount
	r.lastVerifier = verifierAccount
	r.calls++
	return append([]byte(nil), r.body...), nil
}

// ---------------------------------------------------------------------------
// Tests 4 + 5 — transport handler authorization.
// ---------------------------------------------------------------------------
// Implemented in handler_test.go (transport package).

// ---------------------------------------------------------------------------
// Test 6 — TestHealer_FailedSubmitDoesNotPersistDedupRow.
// ---------------------------------------------------------------------------
//
// Crash-recovery contract: SubmitClaim is the source of truth — only when
// the chain has accepted the claim is the SQLite dedup row written. A
// failed submit (mempool full, signing error, chain reject) leaves NO row,
// so the next tick can retry cleanly. Reverse ordering would strand the
// op forever on flaky submits, so this test pins the ordering.
//
// Companion: when chain has already accepted a prior submit but the
// supernode crashed before persisting, reconcileExistingClaim queries
// GetHealOp on resubmit-error and persists the row when ResultHash matches.
// That recovery path is exercised separately.
func TestHealer_FailedSubmitDoesNotPersistDedupRow(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("recovered-payload")
	wantHash := hashOf(t, body)
	h.cascade.reseedFn = func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		// Simulate stageArtefacts side-effect: write reconstructed file +
		// minimal manifest under StagingDir.
		_ = makeStagingDir(t, h.stagingRoot, 20, wantHash, body)
		return &cascadeService.RecoveryReseedResult{
			ActionID:             req.ActionID,
			DataHashVerified:     true,
			ReconstructedHashB64: wantHash,
			StagingDir:           req.StagingDir,
		}, nil
	}
	// Simulate a non-state-error submit failure (e.g. mempool full).
	h.auditMsg.claimErr = errors.New("simulated mempool full")
	h.audit.put(audittypes.HealOp{
		HealOpId:               20,
		TicketId:               "ticket-q",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
		ResultHash:             "",
	})
	_ = h.svc.tick(context.Background())
	// Wait for the goroutine to finish.
	time.Sleep(200 * time.Millisecond)
	// No row should have been written (chain didn't accept).
	has, _ := h.store.HasHealClaim(context.Background(), 20)
	if has {
		t.Fatalf("dedup row must NOT exist when chain submit failed; row found")
	}
	// Staging dir should be cleaned up so the next tick starts fresh.
	stagingDir := filepath.Join(h.stagingRoot, "20")
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed on submit failure; stat err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 6b — TestHealer_ReconcilesExistingChainClaimAfterCrash.
// ---------------------------------------------------------------------------
//
// Crash-recovery: prior submit succeeded but supernode crashed before
// persisting. Resubmit returns "does not accept healer completion claim"
// (chain advanced past SCHEDULED). reconcileExistingClaim must:
//   - re-fetch the heal-op
//   - confirm chain ResultHash equals our manifest
//   - persist the dedup row so finalizer can take over
func TestHealer_ReconcilesExistingChainClaimAfterCrash(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("recovered-payload-22")
	wantHash := hashOf(t, body)
	h.cascade.reseedFn = func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		_ = makeStagingDir(t, h.stagingRoot, 22, wantHash, body)
		return &cascadeService.RecoveryReseedResult{
			ActionID:             req.ActionID,
			DataHashVerified:     true,
			ReconstructedHashB64: wantHash,
			StagingDir:           req.StagingDir,
		}, nil
	}
	// Simulate chain having already accepted a previous submit.
	h.auditMsg.claimErr = errors.New("rpc error: code = Unknown desc = heal op status HEAL_OP_STATUS_HEALER_REPORTED does not accept healer completion claim")
	// Heal-op is in HEALER_REPORTED with our manifest hash.
	h.audit.put(audittypes.HealOp{
		HealOpId:               22,
		TicketId:               "ticket-r",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount: "sn-healer",
		ResultHash:             wantHash,
	})
	// Note: dispatchHealerOps filters on SCHEDULED, so we drive the
	// reconcile path directly via reconstructAndClaim.
	op := audittypes.HealOp{
		HealOpId:               22,
		TicketId:               "ticket-r",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED, // healer's local view
		HealerSupernodeAccount: "sn-healer",
	}
	if err := h.svc.reconstructAndClaim(context.Background(), op); err != nil {
		t.Fatalf("reconstructAndClaim: %v", err)
	}
	has, _ := h.store.HasHealClaim(context.Background(), 22)
	if !has {
		t.Fatalf("reconcile must persist dedup row when chain ResultHash matches manifest")
	}
}

func TestHealer_ReconcileHashMismatchCleansStagingWithoutPersisting(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	body := []byte("recovered-payload-23")
	wantHash := hashOf(t, body)
	stagingDir := makeStagingDir(t, h.stagingRoot, 23, wantHash, body)
	h.audit.put(audittypes.HealOp{
		HealOpId:               23,
		TicketId:               "ticket-s",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount: "sn-healer",
		ResultHash:             "different-manifest",
	})
	if err := h.svc.reconcileExistingClaim(context.Background(), audittypes.HealOp{HealOpId: 23, TicketId: "ticket-s"}, wantHash, stagingDir); err != nil {
		t.Fatalf("reconcileExistingClaim: %v", err)
	}
	has, _ := h.store.HasHealClaim(context.Background(), 23)
	if has {
		t.Fatalf("hash mismatch must not persist dedup row")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed on hash mismatch; stat err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 7 — TestHealer_RaptorQReconstructionFailureSkipsClaim (Scenario C1).
// ---------------------------------------------------------------------------
func TestHealer_RaptorQReconstructionFailureSkipsClaim(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	h.cascade.reseedFn = func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
		return nil, errors.New("RaptorQ decode failed: insufficient symbols")
	}
	h.audit.put(audittypes.HealOp{
		HealOpId:               21,
		TicketId:               "ticket-broken",
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
	})
	_ = h.svc.tick(context.Background())
	// Sleep briefly to let the goroutine run.
	time.Sleep(200 * time.Millisecond)
	if h.auditMsg.claimsCount.Load() != 0 {
		t.Fatalf("expected zero claim submissions; got %d", h.auditMsg.claimsCount.Load())
	}
	has, _ := h.store.HasHealClaim(context.Background(), 21)
	if has {
		t.Fatalf("no row should be persisted on reconstruction failure")
	}
}

// ---------------------------------------------------------------------------
// Test 8 — TestFinalizer_VerifiedTriggersPublishToKAD.
// ---------------------------------------------------------------------------
func TestFinalizer_VerifiedTriggersPublishToKAD(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	hash := hashOf(t, []byte("body"))
	stagingDir := makeStagingDir(t, h.stagingRoot, 30, hash, []byte("body"))
	// Pre-seed the dedup row.
	if err := h.store.RecordHealClaim(context.Background(), 30, "ticket-30", hash, stagingDir); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	h.audit.put(audittypes.HealOp{HealOpId: 30, TicketId: "ticket-30", Status: audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED, ResultHash: hash})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return h.cascade.publishCalls.Load() == 1
	})
	if got := h.cascade.lastPublishedDir.Load().(string); got != stagingDir {
		t.Fatalf("published wrong dir: got %q want %q", got, stagingDir)
	}
	// Row must be deleted after successful publish.
	has, _ := h.store.HasHealClaim(context.Background(), 30)
	if has {
		t.Fatalf("dedup row should be deleted after publish")
	}
	// Staging dir cleaned.
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed after publish; stat err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 9 — TestFinalizer_FailedSkipsPublish_DeletesStaging.
// ---------------------------------------------------------------------------
func TestFinalizer_FailedSkipsPublish_DeletesStaging(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	hash := hashOf(t, []byte("x"))
	stagingDir := makeStagingDir(t, h.stagingRoot, 31, hash, []byte("x"))
	if err := h.store.RecordHealClaim(context.Background(), 31, "ticket-31", hash, stagingDir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.audit.put(audittypes.HealOp{HealOpId: 31, TicketId: "ticket-31", Status: audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		has, _ := h.store.HasHealClaim(context.Background(), 31)
		return !has
	})
	if h.cascade.publishCalls.Load() != 0 {
		t.Fatalf("publish must not be called on FAILED")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging should be removed on FAILED")
	}
}

// ---------------------------------------------------------------------------
// Test 10 — TestFinalizer_ExpiredSkipsPublish_DeletesStaging.
// ---------------------------------------------------------------------------
func TestFinalizer_ExpiredSkipsPublish_DeletesStaging(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	hash := hashOf(t, []byte("y"))
	stagingDir := makeStagingDir(t, h.stagingRoot, 32, hash, []byte("y"))
	if err := h.store.RecordHealClaim(context.Background(), 32, "ticket-32", hash, stagingDir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h.audit.put(audittypes.HealOp{HealOpId: 32, Status: audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		has, _ := h.store.HasHealClaim(context.Background(), 32)
		return !has
	})
	if h.cascade.publishCalls.Load() != 0 {
		t.Fatalf("publish must not be called on EXPIRED")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging should be removed on EXPIRED")
	}
}

func TestFinalizer_NotFoundCleansClaimAndStaging(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	hash := hashOf(t, []byte("pruned"))
	stagingDir := makeStagingDir(t, h.stagingRoot, 33, hash, []byte("pruned"))
	if err := h.store.RecordHealClaim(context.Background(), 33, "ticket-33", hash, stagingDir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		has, _ := h.store.HasHealClaim(context.Background(), 33)
		return !has
	})
	if h.cascade.publishCalls.Load() != 0 {
		t.Fatalf("publish must not be called when chain heal-op is not found")
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging should be removed when heal-op is not found")
	}
}

// ---------------------------------------------------------------------------
// Test 11 — TestService_NoRoleSkipsOp.
// ---------------------------------------------------------------------------
func TestService_NoRoleSkipsOp(t *testing.T) {
	h := newHarness(t, "sn-bystander", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	h.audit.put(audittypes.HealOp{
		HealOpId:                  40,
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount:    "sn-other-healer",
		VerifierSupernodeAccounts: []string{"sn-v1", "sn-v2"},
	})
	h.audit.put(audittypes.HealOp{
		HealOpId:                  41,
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-other-healer",
		VerifierSupernodeAccounts: []string{"sn-v1", "sn-v2"},
		ResultHash:                "any",
	})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if h.cascade.reseedCalls.Load() != 0 {
		t.Fatalf("non-assigned supernode must not reconstruct")
	}
	if h.auditMsg.claimsCount.Load() != 0 || h.auditMsg.verificationsCount.Load() != 0 {
		t.Fatalf("no tx should be submitted by non-assigned supernode")
	}
}

// ---------------------------------------------------------------------------
// Test 12 — TestService_UnspecifiedModeSkipsEntirely.
// ---------------------------------------------------------------------------
func TestService_UnspecifiedModeSkipsEntirely(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED)
	// Even ops we'd otherwise be assigned to.
	h.audit.put(audittypes.HealOp{
		HealOpId:               50,
		Status:                 audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED,
		HealerSupernodeAccount: "sn-healer",
	})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if h.cascade.reseedCalls.Load() != 0 {
		t.Fatalf("UNSPECIFIED mode must skip dispatcher entirely")
	}
}

// ---------------------------------------------------------------------------
// Test 13 — TestService_FinalStateOpsIgnored.
// ---------------------------------------------------------------------------
func TestService_FinalStateOpsIgnored(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	// Even with sn-healer assigned, VERIFIED/FAILED/EXPIRED are filtered out
	// at the dispatcher level (status != SCHEDULED, status != HEALER_REPORTED).
	h.audit.put(audittypes.HealOp{HealOpId: 60, Status: audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED, HealerSupernodeAccount: "sn-healer"})
	h.audit.put(audittypes.HealOp{HealOpId: 61, Status: audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED, HealerSupernodeAccount: "sn-healer"})
	h.audit.put(audittypes.HealOp{HealOpId: 62, Status: audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED, HealerSupernodeAccount: "sn-healer"})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if h.cascade.reseedCalls.Load() != 0 {
		t.Fatalf("final-state ops must not trigger reconstruction")
	}
	if h.auditMsg.claimsCount.Load() != 0 {
		t.Fatalf("no claim submissions for final-state ops")
	}
}

// ---------------------------------------------------------------------------
// Test 14 — TestDedup_RestartDoesNotResubmit.
// ---------------------------------------------------------------------------
func TestDedup_RestartDoesNotResubmit(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	hash := hashOf(t, []byte("body"))
	stagingDir := makeStagingDir(t, h.stagingRoot, 70, hash, []byte("body"))
	// Simulate a prior tick that already persisted + submitted.
	if err := h.store.RecordHealClaim(context.Background(), 70, "ticket-70", hash, stagingDir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// New tick sees op in SCHEDULED (chain hasn't seen the tx in the simulator,
	// but supernode dedup must short-circuit).
	h.audit.put(audittypes.HealOp{HealOpId: 70, TicketId: "ticket-70", Status: audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED, HealerSupernodeAccount: "sn-healer"})
	if err := h.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if h.cascade.reseedCalls.Load() != 0 {
		t.Fatalf("restart must NOT re-run RaptorQ for an already-claimed op")
	}
	if h.auditMsg.claimsCount.Load() != 0 {
		t.Fatalf("restart must NOT resubmit claim tx")
	}
	// And same property for verifier dedup:
	hv := newHarness(t, "sn-verifier", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	if err := hv.store.RecordHealVerification(context.Background(), 71, "sn-verifier", true, hash); err != nil {
		t.Fatalf("seed verification: %v", err)
	}
	hv.audit.put(audittypes.HealOp{
		HealOpId:                  71,
		Status:                    audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		HealerSupernodeAccount:    "sn-h",
		VerifierSupernodeAccounts: []string{"sn-verifier"},
		ResultHash:                hash,
	})
	hv.svc.fetcher = &fakeFetcher{body: []byte("body")}
	if err := hv.svc.tick(context.Background()); err != nil {
		t.Fatalf("tick verifier: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if hv.auditMsg.verificationsCount.Load() != 0 {
		t.Fatalf("restart must NOT resubmit verification tx")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func waitForVerifications(t *testing.T, am *programmableAuditMsg, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if am.verificationsCount.Load() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d verifications; got %d", want, am.verificationsCount.Load())
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition")
}
