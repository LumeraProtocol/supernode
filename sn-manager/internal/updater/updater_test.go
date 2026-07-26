package updater

import "testing"

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
