package system

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func StartAllSupernodes(t *testing.T) []*exec.Cmd {
	return StartSupernodesFromDirs(t, []string{"supernode-data1", "supernode-data2", "supernode-data3"}, "supernode")
}

func StartLEP6Supernodes(t *testing.T) []*exec.Cmd {
	return StartSupernodesFromDirs(t, []string{"supernode-lep6-data1", "supernode-lep6-data2", "supernode-lep6-data3"}, "supernode-lep6")
}

func StartSupernodesFromDirs(t *testing.T, relDataDirs []string, logPrefix string) []*exec.Cmd {
	// Determine the project root (assumes tests run from project root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("unable to get working directory: %v", err)
	}

	dataDirs := make([]string, 0, len(relDataDirs))
	for _, rel := range relDataDirs {
		dataDirs = append(dataDirs, filepath.Join(wd, rel))
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

		logPath := filepath.Join(wd, fmt.Sprintf("%s%d.out", logPrefix, i))
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
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
}
