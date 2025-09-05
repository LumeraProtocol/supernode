package version

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallAndSwitchVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	m := NewManager(dir)

	// create a fake binary file to install
	tmpBin := filepath.Join(dir, "fake-supernode")
	if err := os.WriteFile(tmpBin, []byte("#!/bin/sh\necho supernode\n"), 0755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	// install v1.0.0
	if err := m.InstallVersion("v1.0.0", tmpBin); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !m.IsVersionInstalled("v1.0.0") {
		t.Fatalf("version not installed")
	}
	// set current
	if err := m.SetCurrentVersion("v1.0.0"); err != nil {
		t.Fatalf("set current: %v", err)
	}
	cur, err := m.GetCurrentVersion()
	if err != nil || cur != "v1.0.0" {
		t.Fatalf("current=%q err=%v", cur, err)
	}

	// list should include v1.0.0
	vs, err := m.ListVersions()
	if err != nil || len(vs) == 0 {
		t.Fatalf("list versions: %v %v", vs, err)
	}
}
