package updater

import (
	"testing"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
)

func TestShouldUpdatePolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	u := New(t.TempDir(), cfg, "1.2.0")

	// skip prerelease targets
	if u.ShouldUpdate("1.2.0", "1.2.1-beta") {
		t.Fatalf("should not update to prerelease")
	}
	// same-major minor bump
	if !u.ShouldUpdate("1.2.0", "1.3.0") {
		t.Fatalf("should update within same major")
	}
	// major jump blocked
	if u.ShouldUpdate("1.9.0", "2.0.0") {
		t.Fatalf("should not auto update across major")
	}
	// current is prerelease -> should update to same base stable
	if !u.ShouldUpdate("1.2.1-rc.1", "1.2.1") {
		t.Fatalf("should update prerelease to stable of same base")
	}
}

func TestComputeUpdateNeeds(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Updates.CurrentVersion = "1.2.0"
	u := New(t.TempDir(), cfg, "1.1.0")

	m, s := u.computeUpdateNeeds("1.2.1")
	if !m {
		t.Fatalf("manager should need update within same major when behind")
	}
	if !s {
		t.Fatalf("supernode should need update from 1.2.0 -> 1.2.1")
	}

	// manager newer than latest -> no manager update
	u2 := New(t.TempDir(), cfg, "1.3.0")
	m, s = u2.computeUpdateNeeds("1.2.1")
	if m {
		t.Fatalf("manager should not need update when newer")
	}
	if !s {
		t.Fatalf("supernode should still compare with config version")
	}

	// manager unknown versions should be ignored for manager update
	u3 := New(t.TempDir(), cfg, "dev")
	m, _ = u3.computeUpdateNeeds("1.2.1")
	if m {
		t.Fatalf("manager dev should not trigger manager update")
	}
}
