package kademlia

import "strings"

var requiredVer string

// SetRequiredVersion sets the version that peers must match to be accepted.
func SetRequiredVersion(v string) {
	requiredVer = strings.TrimSpace(v)
}

// requiredVersion returns the configured required version (build-time injected by caller).
func requiredVersion() string {
	return requiredVer
}

// versionMismatch determines if the given peer version is unacceptable.
// Policy: required and peer must both be non-empty and exactly equal.
func versionMismatch(peerVersion string) (required string, mismatch bool) {
	required = requiredVersion()
	peer := strings.TrimSpace(peerVersion)
	if required == "" || peer == "" || peer != required {
		return required, true
	}
	return required, false
}
