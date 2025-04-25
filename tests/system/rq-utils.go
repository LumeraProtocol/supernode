//go:build system_test

package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func StartRQService(t *testing.T) *exec.Cmd {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unable to get working directory: %v", err)
	}

	binPath := filepath.Join(wd, "supernode-data", "rqservice", "target", "release", "rq-service")
	configPath := filepath.Join(wd, "supernode-data", "rqservice", "examples", "rqconfig.toml")

	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("rq-service binary not found at %s; did you run install-rqservice?", binPath)
	}

	cmd := exec.Command(binPath, "-c", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start rq-service: %v", err)
	}

	time.Sleep(2 * time.Second)
	return cmd
}

// StopRQService cleanly kills the started process.
// Call via defer immediately after StartRQService.
func StopRQService(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

// StartSupernode launches the supernode binary in the background using the test data directory.
// It calls t.Fatal on any error, and returns the *exec.Cmd so you can defer StopSupernode.
func StartSupernode(t *testing.T) *exec.Cmd {
	// Determine the project root (assumes tests run from project root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unable to get working directory: %v", err)
	}

	// Paths matching your Makefile and install-sn.sh
	dataDir := filepath.Join(wd, "supernode-data")
	binPath := filepath.Join(dataDir, "supernode")
	configPath := filepath.Join(dataDir, "config.yaml")

	// Ensure the binary exists
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("supernode binary not found at %s; did you run setup-supernode-test?", binPath)
	}

	// Build the command: adjust flags as needed for your supernode CLI
	cmd := exec.Command(binPath,
		"start",
		"--config", configPath,
		"--basedir", dataDir,
	)

	// Pipe logs to test output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the process
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start supernode: %v", err)
	}

	// Give it a moment to initialize; swap for a health-check if available
	time.Sleep(2 * time.Second)

	return cmd
}

// StopSupernode cleanly kills the process started by StartSupernode.
// Call this via defer immediately after StartSupernode.
func StopSupernode(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}
