package system

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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
		filepath.Join(wd, "supernode-data1"),
		filepath.Join(wd, "supernode-data2"),
		filepath.Join(wd, "supernode-data3"),
	}

	cmds := make([]*exec.Cmd, len(dataDirs))
	logFiles := make([]*os.File, 0, len(dataDirs))
	t.Cleanup(func() {
		for _, f := range logFiles {
			_ = f.Close()
		}
	})

	// Start each supernode
	for i, dataDir := range dataDirs {
		if err := resetSupernodeRuntimeState(dataDir); err != nil {
			t.Fatalf("failed to clean runtime state for %s: %v", dataDir, err)
		}

		binPath := filepath.Join(dataDir, "supernode")

		// Ensure the binary exists
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			t.Fatalf("supernode binary not found at %s; did you run the appropriate setup?", binPath)
		}

		// Build and start the command
		cmd := exec.Command(binPath,
			"start",
			"--basedir", dataDir,
		)

		logPath := filepath.Join(wd, fmt.Sprintf("supernode%d.out", i))
		logFile, err := os.Create(logPath)
		if err != nil {
			t.Fatalf("failed to create supernode log file %s: %v", logPath, err)
		}
		logFiles = append(logFiles, logFile)

		cmd.Stdout = io.MultiWriter(os.Stdout, logFile)
		cmd.Stderr = io.MultiWriter(os.Stderr, logFile)

		t.Logf("Starting supernode %d from directory: %s", i+1, dataDir)
		t.Logf("Supernode %d log: %s", i+1, logPath)

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
			stopProcessWithTimeout(cmd, 3*time.Second)
		}
	}
}

func resetSupernodeRuntimeState(dataDir string) error {
	volatileDirs := []string{
		"data",
		"raptorq_files",
		"raptorq_files_test",
	}

	for _, rel := range volatileDirs {
		if err := os.RemoveAll(filepath.Join(dataDir, rel)); err != nil {
			return err
		}
	}

	return nil
}

func stopProcessWithTimeout(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()

	_ = cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-done:
		return
	case <-time.After(timeout):
	}

	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
