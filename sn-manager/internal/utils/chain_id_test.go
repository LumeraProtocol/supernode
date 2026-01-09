package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/github"
	githubtestutil "github.com/LumeraProtocol/supernode/v2/pkg/github/testutil"
)

func TestIsTestnetChainID(t *testing.T) {
	if !IsTestnetChainID("lumera-testnet-2") {
		t.Fatalf("expected testnet chain_id to be detected")
	}
	if IsTestnetChainID("lumera-mainnet-1") {
		t.Fatalf("expected mainnet chain_id to not be detected as testnet")
	}
}

func TestReadSupernodeChainID(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfgDir := filepath.Join(tmp, ".supernode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfgPath := filepath.Join(cfgDir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("lumera:\n  chain_id: lumera-testnet-2\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	chainID, err := ReadSupernodeChainID()
	if err != nil {
		t.Fatalf("ReadSupernodeChainID: %v", err)
	}
	if chainID != "lumera-testnet-2" {
		t.Fatalf("unexpected chain_id: %q", chainID)
	}
}

func TestReadSupernodeChainID_MissingOrEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Missing config file should error.
	if _, err := ReadSupernodeChainID(); err == nil {
		t.Fatalf("expected error when config file is missing")
	}

	cfgDir := filepath.Join(tmp, ".supernode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yml")

	// Present file but missing chain_id should error.
	if err := os.WriteFile(cfgPath, []byte("lumera:\n  grpc_addr: https://grpc.testnet.lumera.io\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := ReadSupernodeChainID(); err == nil {
		t.Fatalf("expected error when chain_id is missing")
	}
}

func TestLatestTestnetRelease_IgnoresDrafts(t *testing.T) {
	client := &githubtestutil.FakeClient{
		Releases: []*github.Release{
			{TagName: "v9.9.9-testnet.1", Draft: true},
			{TagName: "v1.2.3", Draft: false},
			{TagName: "v1.2.4-testnet.2", Draft: false},
		},
	}

	r, err := LatestTestnetRelease(client)
	if err != nil {
		t.Fatalf("LatestTestnetRelease: %v", err)
	}
	if r.TagName != "v1.2.4-testnet.2" {
		t.Fatalf("unexpected release: %q", r.TagName)
	}
}

func TestLatestReleaseForChainID_MainnetUsesStable(t *testing.T) {
	client := &githubtestutil.FakeClient{
		LatestStable: &github.Release{TagName: "v1.2.3"},
		Releases:     []*github.Release{{TagName: "v9.9.9-testnet.1"}},
	}

	r, err := LatestReleaseForChainID(client, "lumera-mainnet-1")
	if err != nil {
		t.Fatalf("LatestReleaseForChainID: %v", err)
	}
	if r.TagName != "v1.2.3" {
		t.Fatalf("unexpected release: %q", r.TagName)
	}
	if client.CallsLatestStable != 1 || client.CallsListReleases != 0 {
		t.Fatalf("unexpected calls: stable=%d list=%d", client.CallsLatestStable, client.CallsListReleases)
	}
}

func TestLatestReleaseForChainID_TestnetIsStrict(t *testing.T) {
	// No testnet-tagged release: should error and not fall back to stable.
	client := &githubtestutil.FakeClient{
		LatestStable: &github.Release{TagName: "v1.2.3"},
		Releases:     []*github.Release{{TagName: "v1.2.3"}},
	}

	if _, err := LatestReleaseForChainID(client, "lumera-testnet-2"); err == nil {
		t.Fatalf("expected error when no testnet releases exist")
	}
	if client.CallsLatestStable != 0 || client.CallsListReleases != 1 {
		t.Fatalf("unexpected calls: stable=%d list=%d", client.CallsLatestStable, client.CallsListReleases)
	}

	// With a testnet-tagged release: should return it.
	client2 := &githubtestutil.FakeClient{
		LatestStable: &github.Release{TagName: "v1.2.3"},
		Releases:     []*github.Release{{TagName: "v1.2.4-testnet.1"}},
	}
	r, err := LatestReleaseForChainID(client2, "lumera-testnet-2")
	if err != nil {
		t.Fatalf("LatestReleaseForChainID: %v", err)
	}
	if r.TagName != "v1.2.4-testnet.1" {
		t.Fatalf("unexpected release: %q", r.TagName)
	}
	if client2.CallsLatestStable != 0 || client2.CallsListReleases != 1 {
		t.Fatalf("unexpected calls: stable=%d list=%d", client2.CallsLatestStable, client2.CallsListReleases)
	}
}
