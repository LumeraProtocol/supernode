package version

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// IsCompatibleSupernodeVersion reports whether the supplied supernode version
// string satisfies the SDK gate (>= MinSupernodeVersion).
//
// Semantics:
//   - A leading "v" is tolerated ("v2.5.0" parses as "2.5.0").
//   - Pre-release identifiers (-rc1, -beta, -alpha.1, +build) are accepted
//     when the base version equals or exceeds the floor. The floor is
//     compared as "MinSupernodeVersion-0" so that ANY pre-release of the
//     target version (e.g. "2.5.0-rc1") is eligible.
//   - Empty, unparseable, or otherwise malformed inputs are treated as
//     INELIGIBLE (fail-closed). A supernode that cannot prove its version
//     is presumed stale; the cost of a single retry against another node
//     is strictly less than the cost of silently rotting an upload.
//
// The function never panics.
func IsCompatibleSupernodeVersion(reported string) bool {
	reported = strings.TrimSpace(reported)
	if reported == "" {
		return false
	}

	got, err := semver.NewVersion(reported)
	if err != nil {
		return false
	}

	floor, err := semver.NewVersion(MinSupernodeVersion + "-0")
	if err != nil {
		// MinSupernodeVersion is a compile-time constant; an unparseable
		// floor is a programmer error. Fail closed rather than letting
		// every supernode through.
		return false
	}

	return !got.LessThan(floor)
}
