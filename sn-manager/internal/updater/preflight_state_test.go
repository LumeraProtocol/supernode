package updater

import (
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
