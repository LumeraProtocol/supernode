package utils

import "testing"

func TestCompareVersions_Core(t *testing.T) {
	t.Parallel()

	cases := []struct {
		v1   string
		v2   string
		want int
	}{
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.10", "1.2.3", 1},
		{"1.10.0", "1.2.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.2", "1.2.0", 0},

		// Build metadata is ignored.
		{"1.0.0+build.1", "1.0.0+build.2", 0},

		// Pre-release has lower precedence than stable.
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0", "1.0.0-testnet.1", 1},

		// Pre-release identifier comparison and length rules.
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric < non-numeric
		{"1.0.0-alpha.beta", "1.0.0-alpha.beta.1", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-alpha.10", "1.0.0-alpha.2", 1},
		{"1.0.0-rc.1", "1.0.0-rc.1+build.5", 0},
	}

	for _, tc := range cases {
		got := CompareVersions(tc.v1, tc.v2)
		if got != tc.want {
			t.Fatalf("CompareVersions(%q, %q)=%d want %d", tc.v1, tc.v2, got, tc.want)
		}
		// Symmetry sanity-check.
		got2 := CompareVersions(tc.v2, tc.v1)
		if got2 != -tc.want {
			t.Fatalf("CompareVersions symmetry: (%q,%q)=%d but reverse=%d", tc.v1, tc.v2, got, got2)
		}
	}
}

func TestSameMajor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		v1   string
		v2   string
		want bool
	}{
		{"v1.2.3", "1.0.0", true},
		{"1.2.3-testnet.1", "1.9.0", true},
		{"1.2.3+build.7", "1.0.0-alpha.1", true},
		{"1.2.3", "2.0.0", false},
	}

	for _, tc := range cases {
		if got := SameMajor(tc.v1, tc.v2); got != tc.want {
			t.Fatalf("SameMajor(%q, %q)=%v want %v", tc.v1, tc.v2, got, tc.want)
		}
	}
}
