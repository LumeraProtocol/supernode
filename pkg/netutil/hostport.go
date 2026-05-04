package netutil

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParseHostAndPort parses a raw host/address into host and port.
//
// Accepted inputs include:
//   - "host" (uses defaultPort)
//   - "host:1234"
//   - "scheme://host:1234/path" (uses URL host portion)
//   - "[2001:db8::1]:1234"
//   - "[2001:db8::1]" (uses defaultPort)
//   - "fe80::1%eth0" (IPv6 literal with zone, uses defaultPort)
//
// If a port is present but invalid, the parser falls back to defaultPort for
// compatibility with the existing storage-challenge address parser.
func ParseHostAndPort(address string, defaultPort int) (host string, port int, ok bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, false
	}

	// If it looks like a URL, parse and use the host[:port] portion.
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		address = u.Host
	}

	if h, p, err := net.SplitHostPort(address); err == nil {
		h = strings.TrimSpace(h)
		if h == "" {
			return "", 0, false
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			return h, n, true
		}
		return h, defaultPort, true
	}

	// No port present. Treat it as a raw host if it is plausibly valid; otherwise fail.
	host = strings.TrimSpace(address)
	if host == "" {
		return "", 0, false
	}

	// Accept bracketed IPv6 literal without a port (e.g. "[2001:db8::1]") by stripping brackets.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && strings.Count(host, "]") == 1 {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		host = strings.TrimSpace(host)
		if host == "" {
			return "", 0, false
		}
	}

	// Reject obviously malformed inputs (paths, fragments, userinfo, whitespace, or stray brackets).
	if strings.ContainsAny(host, " \t\r\n/\\?#@[]") {
		return "", 0, false
	}

	// If it contains ':' it must be a valid IPv6 literal (optionally with a zone, e.g. "fe80::1%eth0").
	if strings.Contains(host, ":") {
		ipPart := host
		if i := strings.IndexByte(ipPart, '%'); i >= 0 {
			ipPart = ipPart[:i]
		}
		if net.ParseIP(ipPart) == nil {
			return "", 0, false
		}
	}

	return host, defaultPort, true
}
