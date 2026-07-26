package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
)

// newTestUpdater constructs an AutoUpdater bound to a temp home directory.
// versionMgr and github client are the real ones — tests either pre-install
// binaries on disk or avoid calling the network code path.
func newTestUpdater(t *testing.T, home string) *AutoUpdater {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Updates.CurrentVersion = "v2.6.0-testnet"
	if err := config.Save(cfg, filepath.Join(home, "config.yml")); err != nil {
		t.Fatalf("save cfg: %v", err)
	}
	return &AutoUpdater{
		config:     cfg,
		homeDir:    home,
		versionMgr: version.NewManager(home),
	}
}

// TestPerformRollback_HappyPath pre-installs the rollback target binary on
// disk (avoiding the network path), then asserts that performRollback:
//   - swaps the current symlink to the rollback target
//   - persists sn-manager config.yml with the rolled-back version
//   - writes a .needs_restart marker
//   - writes ~/.sn-manager/rolled-back.log
func TestPerformRollback_HappyPath(t *testing.T) {
	home := t.TempDir()
	u := newTestUpdater(t, home)

	// Pre-install rollback target so downloadRollbackTarget is NOT invoked.
	target := rollbackTargetVersion
	dir := u.versionMgr.GetVersionDir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "supernode"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	if err := u.performRollback("v2.6.0-testnet", "unit-test reason"); err != nil {
		t.Fatalf("performRollback: %v", err)
	}

	// Symlink activated
	got, err := u.versionMgr.GetCurrentVersion()
	if err != nil {
		t.Fatalf("GetCurrentVersion: %v", err)
	}
	if got != target {
		t.Fatalf("current version = %s, want %s", got, target)
	}

	// Config persisted
	reloaded, err := config.Load(filepath.Join(home, "config.yml"))
	if err != nil {
		t.Fatalf("reload cfg: %v", err)
	}
	if reloaded.Updates.CurrentVersion != target {
		t.Fatalf("config current_version = %s, want %s", reloaded.Updates.CurrentVersion, target)
	}

	// Restart marker
	if _, err := os.Stat(filepath.Join(home, ".needs_restart")); err != nil {
		t.Fatalf(".needs_restart missing: %v", err)
	}

	// Rollback log
	logData, err := os.ReadFile(RollbackLogPath(home))
	if err != nil {
		t.Fatalf("read rollback log: %v", err)
	}
	if len(logData) == 0 {
		t.Fatalf("rollback log is empty")
	}
}

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
