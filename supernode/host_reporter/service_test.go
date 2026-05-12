package host_reporter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeProbeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ipv4 host only", in: "203.0.113.1", want: "203.0.113.1"},
		{name: "host port", in: "example.com:8080", want: "example.com"},
		{name: "ipv6 host only", in: "2001:db8::1", want: "2001:db8::1"},
		{name: "bracketed ipv6 host only", in: "[2001:db8::1]", want: "2001:db8::1"},
		{name: "bracketed ipv6 host port", in: "[2001:db8::1]:8080", want: "2001:db8::1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeProbeHost(tc.in); got != tc.want {
				t.Fatalf("normalizeProbeHost(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCascadeKademliaDBBytes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite := func(name string, size int) {
		p := filepath.Join(dir, name)
		b := make([]byte, size)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	mustWrite("data001.sqlite3", 100)
	mustWrite("data001.sqlite3-wal", 50)
	mustWrite("data001.sqlite3-shm", 25)
	mustWrite("unrelated.txt", 999)

	s := &Service{p2pDataDir: dir}
	got, ok := s.cascadeKademliaDBBytes(context.Background())
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if want := uint64(175); got != want {
		t.Fatalf("cascadeKademliaDBBytes=%d want %d", got, want)
	}
}

func TestCascadeKademliaDBBytes_NoMatches(t *testing.T) {
	t.Parallel()
	s := &Service{p2pDataDir: t.TempDir()}
	_, ok := s.cascadeKademliaDBBytes(context.Background())
	if ok {
		t.Fatalf("expected ok=false when no sqlite db files exist")
	}
}
