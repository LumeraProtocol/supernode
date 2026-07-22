package version

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
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

func TestSetCurrentVersionIfNewerActivatesWhenCurrentMissing(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	src := filepath.Join(home, "src-supernode")
	if err := os.WriteFile(src, []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallVersion("v2.6.1-testnet", src); err != nil {
		t.Fatal(err)
	}
	changed, err := m.SetCurrentVersionIfNewer("v2.6.1-testnet")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("missing current symlink should activate target")
	}
}

func TestSetCurrentVersionIfNewerNeverDowngrades(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	src := filepath.Join(home, "src-supernode")
	if err := os.WriteFile(src, []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v2.5.0-testnet", "v2.6.1-testnet", "v2.7.0-testnet"} {
		if err := m.InstallVersion(version, src); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetCurrentVersion("v2.6.1-testnet"); err != nil {
		t.Fatal(err)
	}

	changed, err := m.SetCurrentVersionIfNewer("v2.5.0-testnet")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("older target must not change active version")
	}
	if got, _ := m.GetCurrentVersion(); got != "v2.6.1-testnet" {
		t.Fatalf("active version downgraded to %q", got)
	}

	changed, err = m.SetCurrentVersionIfNewer("v2.7.0-testnet")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("newer target should activate")
	}
	if got, _ := m.GetCurrentVersion(); got != "v2.7.0-testnet" {
		t.Fatalf("active version = %q", got)
	}
}

func TestInstallVersionSerializesSameTarget(t *testing.T) {
	home := t.TempDir()
	m1, m2 := NewManager(home), NewManager(home)
	a := bytes.Repeat([]byte("a"), 1<<20)
	b := bytes.Repeat([]byte("b"), 1<<20)
	srcA, srcB := filepath.Join(home, "a"), filepath.Join(home, "b")
	if err := os.WriteFile(srcA, a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, b, 0o755); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, item := range []struct {
		manager *Manager
		source  string
	}{{m1, srcA}, {m2, srcB}} {
		wg.Add(1)
		go func(manager *Manager, source string) {
			defer wg.Done()
			errs <- manager.InstallVersion("v2.6.1-testnet", source)
		}(item.manager, item.source)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent install failed: %v", err)
		}
	}
	got, err := os.ReadFile(m1.GetVersionBinary("v2.6.1-testnet"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
		t.Fatal("installed binary is a partial or mixed concurrent write")
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
