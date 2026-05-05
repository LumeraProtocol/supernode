package queries

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbFile := filepath.Join(t.TempDir(), "history.db")
	db, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{createHealClaimsSubmitted, createHealVerificationsSubmitted, createStorageRecheckSubmissions, createRecheckAttemptFailures, createRecheckAttemptFailuresExpiresIndex} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec migration: %v", err)
		}
	}
	return &SQLiteStore{db: db}
}

func TestLEP6_HealClaim_RoundTripAndDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if has, err := s.HasHealClaim(ctx, 42); err != nil || has {
		t.Fatalf("HasHealClaim before insert: has=%v err=%v", has, err)
	}
	if err := s.RecordHealClaim(ctx, 42, "ticket-x", "manifest-h", "/tmp/staging/42"); err != nil {
		t.Fatalf("RecordHealClaim: %v", err)
	}
	// Restart-safety: second insert must be rejected with the typed error.
	err := s.RecordHealClaim(ctx, 42, "ticket-x", "manifest-h", "/tmp/staging/42")
	if !errors.Is(err, ErrLEP6ClaimAlreadyRecorded) {
		t.Fatalf("expected ErrLEP6ClaimAlreadyRecorded on duplicate, got %v", err)
	}
	if has, err := s.HasHealClaim(ctx, 42); err != nil || !has {
		t.Fatalf("HasHealClaim after insert: has=%v err=%v", has, err)
	}
	rec, err := s.GetHealClaim(ctx, 42)
	if err != nil {
		t.Fatalf("GetHealClaim: %v", err)
	}
	if rec.HealOpID != 42 || rec.TicketID != "ticket-x" || rec.ManifestHash != "manifest-h" || rec.StagingDir != "/tmp/staging/42" {
		t.Fatalf("GetHealClaim mismatch: %+v", rec)
	}
	all, err := s.ListHealClaims(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListHealClaims: %v %d", err, len(all))
	}
	if err := s.DeleteHealClaim(ctx, 42); err != nil {
		t.Fatalf("DeleteHealClaim: %v", err)
	}
	if has, err := s.HasHealClaim(ctx, 42); err != nil || has {
		t.Fatalf("HasHealClaim after delete: has=%v err=%v", has, err)
	}
}

func TestLEP6_HealVerification_PerVerifierDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RecordHealVerification(ctx, 7, "sn-a", true, "hash-a"); err != nil {
		t.Fatalf("record A: %v", err)
	}
	// Same heal_op, different verifier — must succeed.
	if err := s.RecordHealVerification(ctx, 7, "sn-b", false, "hash-b"); err != nil {
		t.Fatalf("record B: %v", err)
	}
	// Same (op, verifier) — must dedup.
	err := s.RecordHealVerification(ctx, 7, "sn-a", true, "hash-a")
	if !errors.Is(err, ErrLEP6VerificationAlreadyRecorded) {
		t.Fatalf("expected dedup error, got %v", err)
	}
	if has, err := s.HasHealVerification(ctx, 7, "sn-a"); err != nil || !has {
		t.Fatalf("HasHealVerification(sn-a): has=%v err=%v", has, err)
	}
	if has, err := s.HasHealVerification(ctx, 7, "sn-c"); err != nil || has {
		t.Fatalf("HasHealVerification(sn-c) should be false: has=%v err=%v", has, err)
	}
}

func TestLEP6HealClaimPendingLifecycle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.RecordPendingHealClaim(ctx, 101, "ticket-101", "manifest", "/tmp/stage"))
	has, err := store.HasHealClaim(ctx, 101)
	require.NoError(t, err)
	require.True(t, has)

	err = store.RecordPendingHealClaim(ctx, 101, "ticket-101", "manifest", "/tmp/stage")
	require.ErrorIs(t, err, ErrLEP6ClaimAlreadyRecorded)

	require.NoError(t, store.MarkHealClaimSubmitted(ctx, 101))
	claims, err := store.ListHealClaims(ctx)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, uint64(101), claims[0].HealOpID)
}

func TestLEP6HealVerificationPendingLifecycle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.RecordPendingHealVerification(ctx, 202, "verifier-a", true, "hash"))
	has, err := store.HasHealVerification(ctx, 202, "verifier-a")
	require.NoError(t, err)
	require.True(t, has)

	err = store.RecordPendingHealVerification(ctx, 202, "verifier-a", true, "hash")
	require.ErrorIs(t, err, ErrLEP6VerificationAlreadyRecorded)

	require.NoError(t, store.MarkHealVerificationSubmitted(ctx, 202, "verifier-a"))
	has, err = store.HasHealVerification(ctx, 202, "verifier-a")
	require.NoError(t, err)
	require.True(t, has)
}
