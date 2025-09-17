package lumera

import (
	"testing"
)

func TestGenerateCandidates(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantTLS   int // count
		wantPlain int // count
		wantErr   bool
	}{
		{name: "https no port", input: "https://grpc.testnet.lumera.io", wantTLS: 2, wantPlain: 2},
		{name: "grpcs with port", input: "grpcs://grpc.node9x.com:7443", wantTLS: 1, wantPlain: 1},
		{name: "http no port", input: "http://example.com", wantTLS: 2, wantPlain: 2},
		{name: "no scheme no port", input: "grpc.testnet.lumera.io", wantTLS: 2, wantPlain: 2},
		{name: "no scheme explicit 443", input: "grpc.node9x.com:443", wantTLS: 1, wantPlain: 1},
		{name: "no scheme explicit 9090", input: "grpc.node9x.com:9090", wantTLS: 1, wantPlain: 1},
		{name: "unknown scheme still ok", input: "ftp://invalid.com", wantTLS: 2, wantPlain: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, err := parseAddrMeta(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cands := generateCandidates(meta)
			if len(cands) == 0 {
				t.Fatalf("no candidates generated")
			}
			gotTLS, gotPlain := 0, 0
			seen := map[string]bool{}
			for _, c := range cands {
				if seen[c.target+"|"+boolToStr(c.useTLS)] {
					t.Fatalf("duplicate candidate: %v tls=%v", c.target, c.useTLS)
				}
				seen[c.target+"|"+boolToStr(c.useTLS)] = true
				if c.useTLS {
					gotTLS++
				} else {
					gotPlain++
				}
			}
			if gotTLS != tc.wantTLS || gotPlain != tc.wantPlain {
				t.Fatalf("unexpected counts: got tls=%d plain=%d want tls=%d plain=%d", gotTLS, gotPlain, tc.wantTLS, tc.wantPlain)
			}
		})
	}
}

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func TestGrpcConnectionMethods(t *testing.T) {
	// Test with nil connection
	conn := &grpcConnection{conn: nil}

	// Close should not panic with nil connection
	err := conn.Close()
	if err != nil {
		t.Errorf("Close() with nil connection should return nil, got %v", err)
	}

	// GetConn should return nil
	grpcConn := conn.GetConn()
	if grpcConn != nil {
		t.Errorf("GetConn() with nil connection should return nil, got %v", grpcConn)
	}
}

// no keepalive constants to test anymore
