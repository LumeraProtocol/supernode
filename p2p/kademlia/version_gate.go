package kademlia

import (
	"strconv"
	"strings"
)

// localVer is the advertised version of this binary (e.g., v1.2.3),
// injected by the caller (supernode/cmd) at startup.
var localVer string

// minVer is the optional minimum peer version to accept. If empty, gating is disabled.
var minVer string

// SetLocalVersion sets the version this node advertises to peers.
func SetLocalVersion(v string) {
	localVer = strings.TrimSpace(v)
}

// SetMinVersion sets the optional minimum required peer version for DHT interactions.
// When empty, version gating is disabled and all peers are accepted regardless of version string.
func SetMinVersion(v string) {
	minVer = strings.TrimSpace(v)
}

// localVersion returns the configured advertised version.
func localVersion() string { return localVer }

// minimumVersion returns the configured minimum acceptable version; empty disables gating.
func minimumVersion() string { return minVer }

// versionTooOld reports whether the peerVersion is below the configured minimum version.
// If no minimum is configured, gating is disabled and this returns ("", false).
func versionTooOld(peerVersion string) (minRequired string, tooOld bool) {
	minRequired = minimumVersion()
	if strings.TrimSpace(minRequired) == "" {
		// Gating disabled
		return "", false
	}

	// Normalize inputs (strip leading 'v' and pre-release/build metadata)
	p, okP := parseSemver(peerVersion)
	m, okM := parseSemver(minRequired)
	if !okM {
		// Misconfigured minimum; disable gating to avoid accidental network splits.
		return "", false
	}
	if !okP {
		// Peer did not provide a valid version; treat as too old under a min-version policy.
		return minRequired, true
	}
	// Compare peer >= min
	if p[0] < m[0] {
		return minRequired, true
	}
	if p[0] > m[0] {
		return minRequired, false
	}
	if p[1] < m[1] {
		return minRequired, true
	}
	if p[1] > m[1] {
		return minRequired, false
	}
	if p[2] < m[2] {
		return minRequired, true
	}
	return minRequired, false
}

// parseSemver parses versions like "v1.2.3", "1.2.3-alpha" into [major, minor, patch].
// Returns ok=false if no numeric major part is found.
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	s := strings.TrimSpace(v)
	if s == "" {
		return out, false
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	// Drop pre-release/build metadata
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 {
		return out, false
	}
	// Parse up to 3 numeric parts; missing parts default to 0
	for i := 0; i < len(parts) && i < 3; i++ {
		numStr := parts[i]
		// Trim non-digit suffixes (e.g., "1rc1" -> "1")
		j := 0
		for j < len(numStr) && numStr[j] >= '0' && numStr[j] <= '9' {
			j++
		}
		if j == 0 {
			// No leading digits
			if i == 0 {
				return out, false
			}
			break
		}
		n, err := strconv.Atoi(numStr[:j])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
