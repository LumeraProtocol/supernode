package utils

import (
    "strconv"
    "strings"
)

// CompareVersions compares two semantic versions (SemVer 2.0.0)
// - Handles leading 'v'
// - Ignores build metadata (after '+')
// - Properly compares pre-release identifiers (lower precedence than normal)
// Returns: -1 if v1 < v2, 0 if equal, 1 if v1 > v2
func CompareVersions(v1, v2 string) int {
    p1 := parseSemver(v1)
    p2 := parseSemver(v2)

	// Compare core version
	if p1.major != p2.major {
		if p1.major < p2.major {
			return -1
		}
		return 1
	}
	if p1.minor != p2.minor {
		if p1.minor < p2.minor {
			return -1
		}
		return 1
	}
	if p1.patch != p2.patch {
		if p1.patch < p2.patch {
			return -1
		}
		return 1
	}

    // If core equal, pre-release precedence: absence > presence
    if len(p1.prerelease) == 0 && len(p2.prerelease) == 0 {
    	return 0
    }
	if len(p1.prerelease) == 0 {
		return 1
	}
	if len(p2.prerelease) == 0 {
		return -1
	}

	// Compare pre-release identifiers
	max := len(p1.prerelease)
	if len(p2.prerelease) > max {
		max = len(p2.prerelease)
	}
	for i := 0; i < max; i++ {
		id1 := ""
		id2 := ""
		if i < len(p1.prerelease) {
			id1 = p1.prerelease[i]
		}
		if i < len(p2.prerelease) {
			id2 = p2.prerelease[i]
		}

		if id1 == id2 {
			continue
		}

		n1, e1 := strconv.Atoi(id1)
		n2, e2 := strconv.Atoi(id2)
		if e1 == nil && e2 == nil {
			if n1 < n2 {
				return -1
			}
			return 1
		}
		if e1 == nil && e2 != nil {
			// Numeric identifiers have lower precedence than non-numeric
			return -1
		}
		if e1 != nil && e2 == nil {
			return 1
		}
		if id1 < id2 {
			return -1
		}
		return 1
	}

	// All identifiers equal; shorter set has lower precedence
	if len(p1.prerelease) < len(p2.prerelease) {
		return -1
	}
	if len(p1.prerelease) > len(p2.prerelease) {
		return 1
	}
	return 0
}

// SameMajor reports whether two versions have the same major component.
// It ignores leading 'v', build metadata, and pre-release suffixes when
// determining the major version.
func SameMajor(v1, v2 string) bool {
    p1 := parseSemver(v1)
    p2 := parseSemver(v2)
    return p1.major == p2.major
}

type semverParts struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

func parseSemver(v string) semverParts {
	v = strings.TrimPrefix(v, "v")
	// Strip build metadata
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	core := v
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		pre = v[i+1:]
	}
	maj, min, pat := 0, 0, 0
	parts := strings.Split(core, ".")
	if len(parts) > 0 {
		maj, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		min, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		pat, _ = strconv.Atoi(parts[2])
	}
	var preIDs []string
	if pre != "" {
		preIDs = strings.Split(pre, ".")
	}
	return semverParts{major: maj, minor: min, patch: pat, prerelease: preIDs}
}
