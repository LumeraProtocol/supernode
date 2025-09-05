package utils

import "testing"

func TestCompareVersions_CoreAndPrerelease(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.3-beta", "1.2.3", -1}, // prerelease lower than normal
		{"1.2.3", "1.2.3-beta", 1},
		{"1.2.3-alpha", "1.2.3-beta", -1},
		{"1.2.3-2", "1.2.3-10", -1},
		{"1.2.3-rc.1", "1.2.3-rc.2", -1},
		{"1.2.3+build", "1.2.3+meta", 0}, // build metadata ignored
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Fatalf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSameMajor(t *testing.T) {
	if !SameMajor("1.2.3", "1.9.0") {
		t.Fatal("expected same major")
	}
	if SameMajor("1.2.3", "2.0.0") {
		t.Fatal("expected different major")
	}
	if !SameMajor("v1.0.0-alpha", "1.0.0+build") {
		t.Fatal("expected same major with prefixes and suffixes")
	}
}
