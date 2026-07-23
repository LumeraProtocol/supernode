package updater

import (
	"os"
	"testing"
	"time"
)

// TestWriteReadBlockLogMTime round-trips the sticky block marker so the
// updater can detect operator remediation.
func TestWriteReadBlockLogMTime(t *testing.T) {
	home := t.TempDir()
	mt := time.Date(2026, 7, 6, 8, 40, 0, 0, time.UTC)
	if err := writeBlockLog(home, "reason", "v2.6.0-testnet", mt); err != nil {
		t.Fatalf("writeBlockLog: %v", err)
	}
	got, ok := readBlockLogMTime(home)
	if !ok {
		t.Fatalf("readBlockLogMTime not ok")
	}
	if !got.Equal(mt) {
		t.Fatalf("readBlockLogMTime = %v, want %v", got, mt)
	}

	// clearBlockLog removes it
	clearBlockLog(home)
	if _, ok := readBlockLogMTime(home); ok {
		t.Fatalf("expected block log to be cleared")
	}
}

func TestShouldPreserveBlockOnUnknown(t *testing.T) {
	home := t.TempDir()
	if shouldPreserveBlockOnUnknown(home) {
		t.Fatal("unknown may fail open when no prior block exists")
	}
	blockedMTime := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	if err := writeBlockLog(home, "unprepared", "v2.6.1-testnet", blockedMTime); err != nil {
		t.Fatal(err)
	}

	if !shouldPreserveBlockOnUnknown(home) {
		t.Fatal("an existing confirmed block must survive an inconclusive probe")
	}
}

func TestConfirmedBlockPublishedAfterUnknownWins(t *testing.T) {
	home := t.TempDir()
	if confirmedBlockWins(home, true) {
		t.Fatal("unknown may initially fail open without confirmed evidence")
	}
	if err := writeBlockLog(home, "confirmed concurrently", "v2.6.1-testnet", time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if !confirmedBlockWins(home, true) {
		t.Fatal("confirmed block published after unknown must win before mutation")
	}
	if confirmedBlockWins(home, false) {
		t.Fatal("a definitive allow may clear prior evidence")
	}
}

func TestShouldPreserveBlockOnUnknownWithMalformedMarker(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(BlockLogPath(home), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldPreserveBlockOnUnknown(home) {
		t.Fatal("a malformed existing marker must fail closed on an unknown probe")
	}
}
