// Package version centralises supernode version constants and the SDK-side
// compatibility gate used to refuse offloading work to supernodes that are
// older than the network's current minimum.
//
// Rationale (see PR description / Upgrade-Survival review for v1.12.0):
// the chain upgrades atomically at the halt height. The supernode fleet,
// however, upgrades asynchronously over hours-to-days, since operators
// control their own boxes. During the rollout window an SDK client that
// has already adopted post-upgrade behaviour (e.g. LEP-5 AvailabilityCommitment
// at register, requiring ChunkProof[] at finalize) can be paired with a
// supernode that lacks the matching code path. The resulting action stalls
// in PROCESSING until expiry: per-action data loss, no chain-wide impact,
// but visible to end users as "upload silently broken".
//
// The SDK-side gate is the single lever we have to prevent this: the SDK
// (this module) sits one import-layer above every caller (sdk-go, lumera-
// uploader, sn-api-server, partner clients). Refusing to pair a new client
// with an old supernode here means callers get a clear error and can retry
// against the next candidate.
package version

// MinSupernodeVersion is the lowest supernode software version the SDK will
// accept as an upload target.
//
// IMPORTANT: This constant is the SINGLE SOURCE OF TRUTH for the version
// floor. Do not inline the literal anywhere else; import this constant.
//
// Pre-release suffixes (e.g. -rc1, -rc2, -beta, -beta+meta) on the same
// base version are intentionally treated as eligible — see
// IsCompatibleSupernodeVersion. The floor is "2.5.0-0" semantically, so
// "2.5.0-rc1" satisfies it while "2.4.99" does not.
const MinSupernodeVersion = "2.5.0"
