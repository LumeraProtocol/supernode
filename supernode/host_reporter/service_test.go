package host_reporter

import "testing"

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
