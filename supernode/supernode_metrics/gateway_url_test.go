package supernode_metrics

import "testing"

func TestGatewayStatusURL_IPv4(t *testing.T) {
	got := gatewayStatusURL("203.0.113.1", 8002)
	want := "http://203.0.113.1:8002/api/v1/status"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGatewayStatusURL_IPv6(t *testing.T) {
	got := gatewayStatusURL("2001:db8::1", 8002)
	want := "http://[2001:db8::1]:8002/api/v1/status"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGatewayStatusURL_IPv6BracketedHost(t *testing.T) {
	got := gatewayStatusURL("[2001:db8::1]", 8002)
	want := "http://[2001:db8::1]:8002/api/v1/status"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

