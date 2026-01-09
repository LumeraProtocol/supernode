package version

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListVersions_EmptyWhenMissingDir(t *testing.T) {
	m := NewManager(t.TempDir())

	versions, err := m.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected no versions, got %v", versions)
	}
}

func TestInstallAndActivateVersion(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	src := filepath.Join(home, "src-supernode")
	if err := os.WriteFile(src, []byte("fake-binary"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := m.InstallVersion("v1.2.3-testnet.1", src); err != nil {
		t.Fatalf("InstallVersion: %v", err)
	}
	if !m.IsVersionInstalled("v1.2.3-testnet.1") {
		t.Fatalf("expected version to be installed")
	}

	if err := m.SetCurrentVersion("v1.2.3-testnet.1"); err != nil {
		t.Fatalf("SetCurrentVersion: %v", err)
	}
	got, err := m.GetCurrentVersion()
	if err != nil {
		t.Fatalf("GetCurrentVersion: %v", err)
	}
	if got != "v1.2.3-testnet.1" {
		t.Fatalf("unexpected current version: %q", got)
	}
}

func TestListVersions_SortsNewestFirst(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)

	src := filepath.Join(home, "src-supernode")
	if err := os.WriteFile(src, []byte("fake-binary"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}

	for _, v := range []string{"v1.2.3", "v1.2.10", "v1.2.3-testnet.1"} {
		if err := m.InstallVersion(v, src); err != nil {
			t.Fatalf("InstallVersion(%s): %v", v, err)
		}
	}

	versions, err := m.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %v", versions)
	}
	if versions[0] != "v1.2.10" {
		t.Fatalf("expected newest first, got %v", versions)
	}
}

