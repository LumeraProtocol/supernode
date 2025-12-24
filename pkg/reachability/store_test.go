package reachability

import (
	"net"
	"testing"
	"time"
)

func TestStoreRecordInboundRules(t *testing.T) {
	now := time.Unix(1234, 0)
	ipv4 := net.ParseIP("203.0.113.10")
	ipv6 := net.ParseIP("2001:db8::1")

	cases := []struct {
		name         string
		service      Service
		remoteID     string
		addr         net.Addr
		wantRecorded bool
	}{
		{
			name:         "empty service",
			service:      "",
			remoteID:     "peer",
			addr:         &net.TCPAddr{IP: ipv4, Port: 123},
			wantRecorded: false,
		},
		{
			name:         "nil remote addr",
			service:      ServiceGRPC,
			remoteID:     "peer",
			addr:         nil,
			wantRecorded: false,
		},
		{
			name:         "ipv6 accepted",
			service:      ServiceGRPC,
			remoteID:     "peer",
			addr:         &net.TCPAddr{IP: ipv6, Port: 123},
			wantRecorded: true,
		},
		{
			name:         "loopback accepted",
			service:      ServiceGRPC,
			remoteID:     "peer",
			addr:         &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 123},
			wantRecorded: true,
		},
		{
			name:         "self identity accepted",
			service:      ServiceGRPC,
			remoteID:     "me",
			addr:         &net.TCPAddr{IP: ipv4, Port: 123},
			wantRecorded: true,
		},
		{
			name:         "ipv4 tcp accepted",
			service:      ServiceGRPC,
			remoteID:     "peer",
			addr:         &net.TCPAddr{IP: ipv4, Port: 123},
			wantRecorded: true,
		},
		{
			name:         "ipv4 udp accepted",
			service:      ServiceGateway,
			remoteID:     "",
			addr:         &net.UDPAddr{IP: ipv4, Port: 123},
			wantRecorded: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore()
			store.RecordInbound(tc.service, tc.remoteID, tc.addr, now)

			got := store.LastInbound(tc.service)
			if tc.wantRecorded {
				if got != now {
					t.Fatalf("expected last inbound=%v, got %v", now, got)
				}
				return
			}
			if !got.IsZero() {
				t.Fatalf("expected no record, got %v", got)
			}
		})
	}
}

func TestStoreIsInboundFresh(t *testing.T) {
	store := NewStore()
	now := time.Unix(2000, 0)

	if store.IsInboundFresh(ServiceGRPC, time.Second, now) {
		t.Fatalf("expected not fresh with no evidence")
	}
	if store.IsInboundFresh(ServiceGRPC, 0, now) {
		t.Fatalf("expected not fresh with non-positive window")
	}

	store.RecordInbound(ServiceGRPC, "peer", &net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: 123}, now)

	if !store.IsInboundFresh(ServiceGRPC, time.Second, now.Add(500*time.Millisecond)) {
		t.Fatalf("expected fresh within window")
	}
	if store.IsInboundFresh(ServiceGRPC, time.Second, now.Add(2*time.Second)) {
		t.Fatalf("expected not fresh outside window")
	}
}
