package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func StartAllSupernodes(t *testing.T) []*exec.Cmd {
	// Determine the project root (assumes tests run from project root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unable to get working directory: %v", err)
	}

	// Data directories for all three supernodes
	dataDirs := []string{
		filepath.Join(wd, "supernode-data"),
		filepath.Join(wd, "supernode-data2"),
		filepath.Join(wd, "supernode-data3"),
	}

	cmds := make([]*exec.Cmd, len(dataDirs))

	// Start each supernode
	for i, dataDir := range dataDirs {
		binPath := filepath.Join(dataDir, "supernode")
		configPath := filepath.Join(dataDir, "config.yaml")

		// Ensure the binary exists
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			t.Fatalf("supernode binary not found at %s; did you run the appropriate setup?", binPath)
		}

		// Build and start the command
		cmd := exec.Command(binPath,
			"start",
			"--config", configPath,
			"--basedir", dataDir,
		)

		// Pipe logs to test output
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		t.Logf("Starting supernode %d from directory: %s", i+1, dataDir)

		if err := cmd.Start(); err != nil {
			// Clean up any already started processes before failing
			for j := 0; j < i; j++ {
				if cmds[j] != nil && cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
					_, _ = cmds[j].Process.Wait()
				}
			}
			t.Fatalf("failed to start supernode from %s: %v", dataDir, err)
		}

		cmds[i] = cmd

		// Give it a moment to initialize
		time.Sleep(2 * time.Second)
	}

	return cmds
}

// StopAllSupernodes cleanly stops all supernode instances started by StartAllSupernodes.
// Call this via defer immediately after StartAllSupernodes.
func StopAllSupernodes(cmds []*exec.Cmd) {
	for _, cmd := range cmds {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
}
