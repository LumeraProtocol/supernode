package updater

import (
	"errors"
	"testing"
)

func TestShouldBlockEVMUpgrade(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		target     string
		evmKeyName string
		configErr  error
		want       bool
	}{
		{name: "missing key blocks 2.6 boundary", current: "v2.5.2", target: "v2.6.0", want: true},
		{name: "empty testnet key blocks 2.6 boundary", current: "v2.5.2-testnet", target: "v2.6.0-testnet", evmKeyName: "  ", want: true},
		{name: "config read failure blocks 2.6 boundary", current: "v2.5.2", target: "v2.6.0", configErr: errors.New("read failed"), want: true},
		{name: "configured key allows 2.6 boundary", current: "v2.5.2", target: "v2.6.0", evmKeyName: "evm-key", want: false},
		{name: "missing key does not block 2.5 update", current: "v2.5.1", target: "v2.5.2", want: false},
		{name: "post-migration missing key does not roll back or block", current: "v2.6.0", target: "v2.6.1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldBlockEVMUpgrade(tt.current, tt.target, tt.evmKeyName, tt.configErr); got != tt.want {
				t.Fatalf("shouldBlockEVMUpgrade(%q, %q, %q, %v) = %v, want %v", tt.current, tt.target, tt.evmKeyName, tt.configErr, got, tt.want)
			}
		})
	}
}

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
