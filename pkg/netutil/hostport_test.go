package netutil

import "testing"

func TestParseHostAndPort(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		defaultPort int
		wantHost    string
		wantPort    int
		wantOK      bool
	}{
		{name: "host without port", address: "sn.example.com", defaultPort: 9090, wantHost: "sn.example.com", wantPort: 9090, wantOK: true},
		{name: "host with port", address: "sn.example.com:1234", defaultPort: 9090, wantHost: "sn.example.com", wantPort: 1234, wantOK: true},
		{name: "url host portion", address: "grpc://sn.example.com:2345/path", defaultPort: 9090, wantHost: "sn.example.com", wantPort: 2345, wantOK: true},
		{name: "bracketed ipv6 with port", address: "[2001:db8::1]:3456", defaultPort: 9090, wantHost: "2001:db8::1", wantPort: 3456, wantOK: true},
		{name: "bracketed ipv6 without port", address: "[2001:db8::1]", defaultPort: 9090, wantHost: "2001:db8::1", wantPort: 9090, wantOK: true},
		{name: "ipv6 with zone", address: "fe80::1%eth0", defaultPort: 9090, wantHost: "fe80::1%eth0", wantPort: 9090, wantOK: true},
		{name: "invalid port falls back", address: "sn.example.com:notaport", defaultPort: 9090, wantHost: "sn.example.com", wantPort: 9090, wantOK: true},
		{name: "empty", address: "  ", defaultPort: 9090, wantOK: false},
		{name: "path rejected", address: "sn.example.com/path", defaultPort: 9090, wantOK: false},
		{name: "userinfo rejected", address: "user@sn.example.com", defaultPort: 9090, wantOK: false},
		{name: "stray bracket rejected", address: "sn.example.com]", defaultPort: 9090, wantOK: false},
		{name: "malformed ipv6 rejected", address: "2001:db8:::bad", defaultPort: 9090, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHost, gotPort, gotOK := ParseHostAndPort(tt.address, tt.defaultPort)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotHost != tt.wantHost || gotPort != tt.wantPort {
				t.Fatalf("ParseHostAndPort() = (%q, %d, %v), want (%q, %d, %v)", gotHost, gotPort, gotOK, tt.wantHost, tt.wantPort, tt.wantOK)
			}
		})
	}
}
