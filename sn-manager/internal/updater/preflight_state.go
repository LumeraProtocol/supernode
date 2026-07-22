package updater

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	updateBlockedLogName = "update-blocked.log"
	// Retained so status can display markers written by v2.6.1. New versions
	// never create this marker because automatic rollback has been removed.
	rolledBackLogName = "rolled-back.log"
)

// Paths are exposed via helpers so cmd/status.go can read current block state
// and historical v2.6.1 rollback state without duplicating location literals.
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

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
