package queries

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/jmoiron/sqlx"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbFile := filepath.Join(t.TempDir(), "history.db")
	db, err := sqlx.Connect("sqlite3", dbFile)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{createHealClaimsSubmitted, createHealVerificationsSubmitted} {
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
