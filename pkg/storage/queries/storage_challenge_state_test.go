package queries

import (
	"context"
	"testing"
)

// TestStorageChallengeState_M9_RoundTripPersistsAcrossOpen pins LEP-6 review
// M9 (Matee, 2026-05-06): the storage-challenge dispatcher's last-submitted
// epoch must survive a process restart so we don't re-dispatch the most
// recent epoch on startup. Round-trip through OpenHistoryDBAt twice.
func TestStorageChallengeState_M9_RoundTripPersistsAcrossOpen(t *testing.T) {
	baseDir := t.TempDir()
	ctx := context.Background()

	// First open: write the persisted epoch.
	store1, err := OpenHistoryDBAt(baseDir)
	if err != nil {
		t.Fatalf("OpenHistoryDBAt initial: %v", err)
	}

	// Fresh DB → no row.
	if _, ok, err := store1.GetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey); err != nil {
		t.Fatalf("GetStorageChallengeState fresh: %v", err)
	} else if ok {
		t.Fatalf("fresh DB must have no persisted last-epoch row")
	}

	const persistedEpoch uint64 = 4242
	if err := store1.SetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey, persistedEpoch); err != nil {
		t.Fatalf("SetStorageChallengeState: %v", err)
	}
	store1.CloseHistoryDB(ctx)

	// Second open: read back must succeed with the persisted value.
	store2, err := OpenHistoryDBAt(baseDir)
	if err != nil {
		t.Fatalf("OpenHistoryDBAt reopen: %v", err)
	}
	defer store2.CloseHistoryDB(ctx)

	got, ok, err := store2.GetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey)
	if err != nil {
		t.Fatalf("GetStorageChallengeState reopen: %v", err)
	}
	if !ok {
		t.Fatalf("M9 regression: persisted last-epoch row missing after reopen")
	}
	if got != persistedEpoch {
		t.Fatalf("M9 regression: persisted epoch mismatch: got %d, want %d", got, persistedEpoch)
	}

	// Idempotent upsert: writing the same value again must not error.
	if err := store2.SetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey, persistedEpoch); err != nil {
		t.Fatalf("idempotent upsert: %v", err)
	}
	// Updating to a higher value works.
	if err := store2.SetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey, persistedEpoch+1); err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	got2, _, _ := store2.GetStorageChallengeState(ctx, LEP6LastSubmittedEpochKey)
	if got2 != persistedEpoch+1 {
		t.Fatalf("update did not stick: got %d, want %d", got2, persistedEpoch+1)
	}
}
