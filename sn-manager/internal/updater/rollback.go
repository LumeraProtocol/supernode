package updater

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/utils"
)

// rollbackTargetVersion is the HARDCODED rollback target — the last pre-EVM
// testnet release. This is intentionally not dynamic and not configurable:
// the incident that motivated this code shipped because operators trusted
// the update pipeline to pick a target automatically.
const rollbackTargetVersion = "v2.5.0-testnet"

const (
	updateBlockedLogName = "update-blocked.log"
	rolledBackLogName    = "rolled-back.log"
)

// blockLogPath / rollbackLogPath / etc. are exposed via helpers so cmd/status.go
// can read them without duplicating the location literals.
func BlockLogPath(homeDir string) string    { return filepath.Join(homeDir, updateBlockedLogName) }
func RollbackLogPath(homeDir string) string { return filepath.Join(homeDir, rolledBackLogName) }

// writeBlockLog records a preflight block. Written atomically. The mtime of
// the supernode config at the moment of the block is captured so subsequent
// check cycles can detect operator remediation.
func writeBlockLog(homeDir, reason, target string, snCfgMTime time.Time) error {
	body := fmt.Sprintf(
		"blocked_at: %s\ntarget_version: %s\nreason: %s\nsupernode_config_mtime: %s\nmigration_doc: https://github.com/LumeraProtocol/supernode/blob/master/docs/evm-migration.md\n",
		time.Now().UTC().Format(time.RFC3339),
		target,
		reason,
		snCfgMTime.UTC().Format(time.RFC3339Nano),
	)
	return writeAtomic(BlockLogPath(homeDir), []byte(body))
}

// clearBlockLog is used when the operator has fixed their config (mtime
// advanced past what the block log recorded) so a subsequent update attempt
// is no longer blocked by the sticky marker.
func clearBlockLog(homeDir string) {
	if err := os.Remove(BlockLogPath(homeDir)); err != nil && !os.IsNotExist(err) {
		log.Printf("preflight: failed to remove stale block log: %v", err)
	}
}

// readBlockLogMTime returns the supernode_config_mtime recorded in an existing
// block log, or the zero time + false if no block log is present or parsable.
func readBlockLogMTime(homeDir string) (time.Time, bool) {
	data, err := os.ReadFile(BlockLogPath(homeDir))
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range splitLines(string(data)) {
		const prefix = "supernode_config_mtime: "
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			t, err := time.Parse(time.RFC3339Nano, line[len(prefix):])
			if err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func writeRollbackLog(homeDir, from, to, reason string) error {
	body := fmt.Sprintf(
		"rolled_back_at: %s\nrolled_back_from: %s\nrolled_back_to: %s\nreason: %s\nmigration_doc: https://github.com/LumeraProtocol/supernode/blob/master/docs/evm-migration.md\n",
		time.Now().UTC().Format(time.RFC3339),
		from,
		to,
		reason,
	)
	return writeAtomic(RollbackLogPath(homeDir), []byte(body))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// performRollback installs (downloading if necessary) rollbackTargetVersion,
// swaps the current symlink to it, updates sn-manager config, writes the
// rollback log, and drops a .needs_restart marker so the manager monitor
// picks up the swap on its next tick.
//
// The from/reason strings are used for the log only.
func (u *AutoUpdater) performRollback(from, reason string) error {
	target := rollbackTargetVersion

	// 1. Ensure the target binary is installed. If not, download it.
	if !u.versionMgr.IsVersionInstalled(target) {
		if err := u.downloadRollbackTarget(target); err != nil {
			return fmt.Errorf("download rollback target %s: %w", target, err)
		}
	}

	// 2. Activate.
	if err := u.versionMgr.SetCurrentVersion(target); err != nil {
		return fmt.Errorf("activate rollback target %s: %w", target, err)
	}

	// 3. Persist in sn-manager config.
	u.config.Updates.CurrentVersion = target
	if err := config.Save(u.config, filepath.Join(u.homeDir, "config.yml")); err != nil {
		return fmt.Errorf("save sn-manager config: %w", err)
	}

	// 4. Rollback log + restart marker.
	if err := writeRollbackLog(u.homeDir, from, target, reason); err != nil {
		log.Printf("rollback: failed to write rollback log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(u.homeDir, ".needs_restart"), []byte(target), 0o644); err != nil {
		log.Printf("rollback: failed to write restart marker: %v", err)
	}
	log.Printf("rollback: reverted supernode %s → %s (%s)", from, target, reason)
	return nil
}

// downloadRollbackTarget fetches the rollback release tarball, extracts the
// supernode binary into a temp file, and hands it to versionMgr.InstallVersion.
// Reuses the same tarball-based pipeline as checkAndUpdateCombined; keeps the
// rollback release binary at ~/.sn-manager/binaries/<target>/supernode.
func (u *AutoUpdater) downloadRollbackTarget(target string) error {
	tarURL, err := u.githubClient.GetReleaseTarballURL(target)
	if err != nil {
		return fmt.Errorf("get tarball URL: %w", err)
	}

	downloadsDir := filepath.Join(u.homeDir, "downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir downloads: %w", err)
	}

	tarPath := filepath.Join(downloadsDir, fmt.Sprintf("rollback-%s.tar.gz", target))
	if err := utils.DownloadFile(tarURL, tarPath, nil); err != nil {
		return fmt.Errorf("download tarball: %w", err)
	}
	defer func() {
		if err := os.Remove(tarPath); err != nil && !os.IsNotExist(err) {
			log.Printf("rollback: failed to remove tarball: %v", err)
		}
	}()

	tmpSN := filepath.Join(downloadsDir, fmt.Sprintf("supernode-%s.tmp", target))
	targets := map[string]string{"supernode": tmpSN}
	found, err := utils.ExtractMultipleFromTarGz(tarPath, targets)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	if !found["supernode"] {
		return fmt.Errorf("supernode binary not found in rollback tarball %s", target)
	}
	defer func() {
		if err := os.Remove(tmpSN); err != nil && !os.IsNotExist(err) {
			log.Printf("rollback: failed to remove temp supernode: %v", err)
		}
	}()

	if err := u.versionMgr.InstallVersion(target, tmpSN); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	return nil
}
