package host_reporter

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

// LEP-6 review regression: LEP-6 review M6 (Matee, 2026-05-06). probeTCP must distinguish
// canonical CLOSED (ECONNREFUSED) from operator-side faults (DNS, host
// unreach, ctx errors, timeouts) which now report UNKNOWN.

// TestProbeTCP_M6_OpenPortReturnsOpen exercises the happy path: a listener
// bound to 127.0.0.1:<picked> answers the dial → PORT_STATE_OPEN.
func TestProbeTCP_M6_OpenPortReturnsOpen(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept goroutine: silently close any inbound conn.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	got := probeTCP(context.Background(), "127.0.0.1", uint32(port), 2*time.Second)
	if got != audittypes.PortState_PORT_STATE_OPEN {
		t.Fatalf("M6 happy path: got %s, want OPEN", got.String())
	}
}

// TestProbeTCP_M6_RefusedReturnsClosed pins ECONNREFUSED → CLOSED. We bind
// a port, close the listener, then dial — kernel issues RST.
func TestProbeTCP_M6_RefusedReturnsClosed(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	ln.Close() // close immediately so subsequent dials get RST

	got := probeTCP(context.Background(), "127.0.0.1", uint32(port), 2*time.Second)
	if got != audittypes.PortState_PORT_STATE_CLOSED {
		t.Fatalf("M6 refused: got %s, want CLOSED (ECONNREFUSED is the canonical closed signal)", got.String())
	}
}

// TestProbeTCP_M6_DNSFailureReturnsUnknown — before this fix a DNS resolution
// failure mapped to CLOSED, falsely accusing the peer's port of being shut.
// Now must map to UNKNOWN.
func TestProbeTCP_M6_DNSFailureReturnsUnknown(t *testing.T) {
	t.Parallel()

	// .invalid is reserved by RFC 2606 for DNS-resolution-failure tests;
	// no resolver should ever return an A record.
	got := probeTCP(context.Background(), "no-such-host.invalid", 9999, 2*time.Second)
	if got != audittypes.PortState_PORT_STATE_UNKNOWN {
		t.Fatalf("M6 DNS fail: got %s, want UNKNOWN", got.String())
	}
}

// TestProbeTCP_M6_CtxCanceledReturnsUnknown pins ctx cancellation → UNKNOWN.
func TestProbeTCP_M6_CtxCanceledReturnsUnknown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before dial
	got := probeTCP(ctx, "127.0.0.1", 9999, 2*time.Second)
	if got != audittypes.PortState_PORT_STATE_UNKNOWN {
		t.Fatalf("M6 canceled ctx: got %s, want UNKNOWN", got.String())
	}
}

// TestProbeTCP_M6_DialTimeoutReturnsUnknown pins net.Error.Timeout → UNKNOWN.
// We dial a non-routable host (TEST-NET-1) with a tiny timeout.
func TestProbeTCP_M6_DialTimeoutReturnsUnknown(t *testing.T) {
	t.Parallel()

	// 192.0.2.0/24 is RFC 5737 TEST-NET-1, guaranteed not-routable.
	got := probeTCP(context.Background(), "192.0.2.1", 9999, 50*time.Millisecond)
	if got != audittypes.PortState_PORT_STATE_UNKNOWN {
		t.Fatalf("M6 dial timeout: got %s, want UNKNOWN", got.String())
	}
}
