package updater

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
	githubtestutil "github.com/LumeraProtocol/supernode/v2/pkg/github/testutil"
	managerconfig "github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
)

func TestShouldUpdate_TestnetTagAdvances(t *testing.T) {
	u := &AutoUpdater{}

	if !u.ShouldUpdate("v1.2.3-testnet.1", "v1.2.3-testnet.2") {
		t.Fatalf("expected update for testnet tag bump")
	}
	if !u.ShouldUpdate("v1.2.3-testnet.2", "v1.2.4-testnet.1") {
		t.Fatalf("expected update for testnet patch bump")
	}
}

func TestShouldUpdate_TestnetDoesNotDowngradeFromStable(t *testing.T) {
	u := &AutoUpdater{}

	// SemVer: 1.2.3 (stable) is higher precedence than 1.2.3-testnet.1
	if u.ShouldUpdate("v1.2.3", "v1.2.3-testnet.1") {
		t.Fatalf("expected no update from stable to prerelease")
	}
}

func TestShouldUpdate_StableIgnoresPrerelease(t *testing.T) {
	u := &AutoUpdater{}

	if u.ShouldUpdate("v1.2.2", "v1.2.3-rc.1") {
		t.Fatalf("expected prerelease targets to be ignored for stable channel")
	}
}

func TestShouldUpdate_StableWithinMajor(t *testing.T) {
	u := &AutoUpdater{}

	if !u.ShouldUpdate("v1.2.2", "v1.2.3") {
		t.Fatalf("expected stable update within major")
	}
	if u.ShouldUpdate("v1.2.3", "v2.0.0") {
		t.Fatalf("expected major jumps to be rejected")
	}
}

func TestShouldUpdate_PrereleaseToStableSameBase(t *testing.T) {
	u := &AutoUpdater{}

	if !u.ShouldUpdate("v1.2.3-alpha.1", "v1.2.3") {
		t.Fatalf("expected prerelease to stable update for same base")
	}
}

func TestShouldForceUpdate_OnlyMovesForward(t *testing.T) {
	cases := []struct {
		name    string
		current string
		target  string
		want    bool
	}{
		{"upgrade", "v2.6.0-testnet", "v2.6.1-testnet", true},
		{"equal", "v2.6.1-testnet", "v2.6.1-testnet", false},
		{"downgrade", "v2.6.1-testnet", "v2.5.0-testnet", false},
		{"stable to prerelease", "v2.6.1", "v2.6.1-rc.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldForceUpdate(tc.current, tc.target); got != tc.want {
				t.Fatalf("shouldForceUpdate(%q, %q) = %v, want %v", tc.current, tc.target, got, tc.want)
			}
		})
	}
}

func TestCheckAndUpdateCombined_ConfigReadFailureDoesNotSelectReleaseChannel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	client := &githubtestutil.FakeClient{
		LatestStable: &github.Release{TagName: "v2.6.1"},
	}
	u := &AutoUpdater{
		config:         &managerconfig.Config{Updates: managerconfig.UpdateConfig{CurrentVersion: "v2.6.1"}},
		homeDir:        t.TempDir(),
		githubClient:   client,
		managerVersion: "v2.6.1",
	}

	u.checkAndUpdateCombined(true)

	if client.CallsLatestStable != 0 || client.CallsListReleases != 0 {
		t.Fatalf("config read failure must abort before release selection; stable=%d list=%d", client.CallsLatestStable, client.CallsListReleases)
	}
}

func TestCheckAndUpdateCombined_UsesActiveSymlinkNotStaleConfig(t *testing.T) {
	supernodeHome := t.TempDir()
	t.Setenv("HOME", supernodeHome)
	if err := os.MkdirAll(filepath.Join(supernodeHome, ".supernode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supernodeHome, ".supernode", "config.yml"), []byte("lumera:\n  chain_id: lumera-testnet-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	managerHome := t.TempDir()
	versionMgr := version.NewManager(managerHome)
	source := filepath.Join(managerHome, "supernode")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := versionMgr.InstallVersion("v2.6.1-testnet", source); err != nil {
		t.Fatal(err)
	}
	if err := versionMgr.SetCurrentVersion("v2.6.1-testnet"); err != nil {
		t.Fatal(err)
	}

	client := &githubtestutil.FakeClient{Releases: []*github.Release{{TagName: "v2.5.1-testnet"}}}
	u := &AutoUpdater{
		config:         &managerconfig.Config{Updates: managerconfig.UpdateConfig{CurrentVersion: "v2.5.0-testnet"}},
		homeDir:        managerHome,
		githubClient:   client,
		versionMgr:     versionMgr,
		managerVersion: "v2.6.1-testnet",
	}

	u.checkAndUpdateCombined(true)

	if client.CallsTarballURL != 0 {
		t.Fatal("automatic update used stale config and attempted to download an older release")
	}
	if active, err := versionMgr.GetCurrentVersion(); err != nil || active != "v2.6.1-testnet" {
		t.Fatalf("active version = %q, err=%v", active, err)
	}
}
