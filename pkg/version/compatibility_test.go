package version

import (
	"testing"
)

func TestIsCompatibleSupernodeVersion(t *testing.T) {
	tests := []struct {
		name     string
		reported string
		want     bool
	}{
		// --- I1: floor and above are eligible ---
		{"floor exact 2.5.0", "2.5.0", true},
		{"floor with v prefix", "v2.5.0", true},
		{"patch above floor", "2.5.1", true},
		{"minor above floor", "2.6.0", true},
		{"major above floor", "3.0.0", true},
		{"way above floor", "10.20.30", true},

		// --- I4: 2.5.0 pre-releases are eligible per product decision ---
		{"rc1 of floor", "2.5.0-rc1", true},
		{"rc2 of floor with v", "v2.5.0-rc2", true},
		{"rc with build meta", "2.5.0-rc2+build.5", true},
		{"beta of floor", "2.5.0-beta", true},
		{"alpha numeric", "2.5.0-alpha.1", true},
		// Boundary: semver -0 prerelease is the lowest possible 2.5.0-*.
		{"floor with -0 prerelease boundary", "2.5.0-0", true},

		// --- I4 negative: pre-release of an older base is NOT eligible.
		// 2.4.99-rc1 < 2.5.0-0 in semver, so it must be rejected.
		{"rc of pre-floor version", "2.4.99-rc1", false},
		{"beta of pre-floor version", "2.4.72-beta", false},

		// --- I1 negative: older versions are not eligible ---
		{"one patch below floor", "2.4.72", false},
		{"latest pre-2.5 hotfix", "2.4.72", false},
		{"pre-2.x", "1.99.99", false},
		{"zero version", "0.0.0", false},

		// --- I2: malformed / empty inputs fail closed ---
		{"empty string", "", false},
		{"only whitespace", "   ", false},
		{"garbage", "not-a-version", false},
		// Masterminds/semver/v3 accepts partial inputs by padding zeros:
		// "2.5" -> 2.5.0, "2" -> 2.0.0. The eligibility outcome is then
		// driven purely by the resulting normalized version vs the floor.
		{"incomplete major-minor parses to 2.5.0 (== floor)", "2.5", true},
		{"truncated to major parses to 2.0.0 (< floor)", "2", false},
		{"nonsense letters", "abc.def.ghi", false},
		{"negative numbers", "-1.0.0", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCompatibleSupernodeVersion(tc.reported)
			if got != tc.want {
				t.Fatalf("IsCompatibleSupernodeVersion(%q) = %v, want %v", tc.reported, got, tc.want)
			}
		})
	}
}

// TestMinSupernodeVersion_IsValidSemver guards against accidentally setting
// MinSupernodeVersion to a value that is unparseable, which would cause
// every IsCompatibleSupernodeVersion call to fail-closed and effectively
// brick the SDK.
func TestMinSupernodeVersion_IsValidSemver(t *testing.T) {
	// IsCompatibleSupernodeVersion(MinSupernodeVersion) must be true:
	// the constant itself is the floor and an exact match is eligible.
	if !IsCompatibleSupernodeVersion(MinSupernodeVersion) {
		t.Fatalf("MinSupernodeVersion=%q is not self-compatible; floor is unparseable or wrong", MinSupernodeVersion)
	}
}
