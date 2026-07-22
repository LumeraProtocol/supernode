package updater

import (
	"context"
	"testing"
)

func TestDecidePreflight_Table(t *testing.T) {
	cases := []struct {
		name string
		in   preflightInputs
		want preflightDecision
	}{
		{"no-evm chain forward", preflightInputs{false, "", "v2.5.0-testnet", "v2.6.0-testnet"}, preflightAllow},
		{"no-evm chain already 2.6", preflightInputs{false, "", "v2.6.0-testnet", "v2.6.0-testnet"}, preflightAllow},
		{"evm no-key forward to 2.6", preflightInputs{true, "", "v2.5.0-testnet", "v2.6.0-testnet"}, preflightBlock},
		{"evm migrated config at 2.6 omits transitional key", preflightInputs{true, "", "v2.6.0-testnet", "v2.6.0-testnet"}, preflightAllow},
		{"evm migrated config can update beyond 2.6", preflightInputs{true, "", "v2.6.0-testnet", "v2.6.1-testnet"}, preflightAllow},
		{"evm prepared forward to 2.6", preflightInputs{true, "evm-key", "v2.5.0-testnet", "v2.6.0-testnet"}, preflightAllow},
		{"evm prepared at 2.6", preflightInputs{true, "evm-key", "v2.6.0-testnet", "v2.6.0-testnet"}, preflightAllow},
		{"evm no-key below threshold 2.5.1", preflightInputs{true, "", "v2.5.0-testnet", "v2.5.1-testnet"}, preflightAllow},
		{"evm no-key below threshold 2.5.0", preflightInputs{true, "", "v2.4.5-testnet", "v2.5.0-testnet"}, preflightAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := decidePreflight(tc.in)
			if got != tc.want {
				t.Fatalf("decidePreflight(%+v) = %v (%s), want %v", tc.in, got, reason, tc.want)
			}
		})
	}
}

func TestParseGRPCAddr(t *testing.T) {
	cases := []struct {
		in         string
		wantHost   string
		wantTLS    bool
		wantErrSub string
	}{
		{"grpc.testnet.lumera.io:443", "grpc.testnet.lumera.io:443", true, ""},
		{"grpc.testnet.lumera.io:9090", "grpc.testnet.lumera.io:9090", false, ""},
		{"https://grpc.testnet.lumera.io", "grpc.testnet.lumera.io:443", true, ""},
		{"https://grpc.testnet.lumera.io:8443", "grpc.testnet.lumera.io:8443", true, ""},
		{"http://localhost:9090", "localhost:9090", false, ""},
		{"localhost:9090", "localhost:9090", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			h, tls, err := parseGRPCAddr(tc.in)
			if err != nil {
				t.Fatalf("parseGRPCAddr(%q) err=%v", tc.in, err)
			}
			if h != tc.wantHost || tls != tc.wantTLS {
				t.Fatalf("parseGRPCAddr(%q) = (%q,%v), want (%q,%v)", tc.in, h, tls, tc.wantHost, tc.wantTLS)
			}
		})
	}
}

// TestQueryEVMModuleActive_FailOpen simulates chain unreachability by passing
// an empty grpc_addr and asserts that the caller sees an error (which the
// preflight then treats as fail-open at the AutoUpdater level).
func TestQueryEVMModuleActive_FailOpenOnEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := queryEVMModuleActive(ctx, "")
	if err == nil {
		t.Fatalf("expected error for empty grpc_addr")
	}
}
