// Package chainerrors centralises classification of Lumera chain errors as
// surfaced to the supernode runtime.
//
// Background:
//
// The audit module on the chain uses cosmossdk.io/errors registered errors
// (e.g. audittypes.ErrHealOpInvalidState). Tx rejections come back through
// the cosmos tx pipeline carrying ABCI (codespace, code, raw_log) tuples;
// the supernode tx layer reconstructs the typed error via errorsmod.ABCIError
// and wraps it with %w so that errors.Is(err, audittypes.ErrXxx) works for
// callers (see pkg/lumera/modules/tx/impl.go BroadcastTransaction).
//
// Query rejections (gRPC) come back as standard google.golang.org/grpc/status
// errors, e.g. status.Error(codes.NotFound, "heal op not found") for the
// HealOp query in x/audit/v1/keeper/query_storage_truth.go.
//
// The predicates here:
//
//   1. Prefer typed sentinel matching via errors.Is.
//   2. Fall through to gRPC status codes for query-side rejections.
//   3. Keep an English-substring fallback so we remain correct against any
//      currently-deployed chain build whose error path doesn't preserve the
//      typed sentinel through the wire (defense-in-depth, removable once
//      every chain build in production guarantees end-to-end ABCIError).
//
// IsTransientGrpc is the safety valve: any path that classifies an error as
// "definitely a chain-side reject" (and would therefore destructively clean
// up local state) MUST first check IsTransientGrpc and bail to retry on true.
package chainerrors

import (
	"context"
	"errors"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsHealOpInvalidState reports whether err corresponds to the chain rejecting
// a heal-op state transition (e.g. "heal op status %s does not accept healer
// completion claim", "verification_hash is required", "heal op has no
// independent verifier assignments").
//
// This is the chain's signal that our submit attempt was structurally
// invalid for the op's current chain state — it is NOT a transient error,
// callers may proceed to reconcile via GetHealOp.
func IsHealOpInvalidState(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, audittypes.ErrHealOpInvalidState) {
		return true
	}
	// Substring fallback — match the discriminating phrase from
	// x/audit/v1/keeper/msg_storage_truth.go:231.
	return strings.Contains(err.Error(), "does not accept healer completion claim")
}

// IsHealOpNotFound reports whether err corresponds to the chain reporting
// the queried heal op does not exist. This maps to BOTH:
//
//   - gRPC status.Code(err) == codes.NotFound from query_storage_truth.go:78
//   - audittypes.ErrHealOpNotFound (registered code 11) from tx-side guards
//     in msg_storage_truth.go:222, :278
//
// Callers MUST first verify with IsTransientGrpc(err) — older code paths
// matched any error containing "not found" (gRPC "block N not found", codec
// lookup miss, key-not-found inside Cosmos SDK), which led to destructive
// cleanup on transient query failures.
func IsHealOpNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, audittypes.ErrHealOpNotFound) {
		return true
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		// Anchor on the chain's exact NotFound message to avoid catching
		// unrelated "not found" errors that happen to be wrapped in a gRPC
		// NotFound status.
		return strings.Contains(st.Message(), "heal op not found")
	}
	// Final substring fallback — kept narrow on purpose (must contain
	// "heal op" to avoid the broad "not found"/"not_found" trap from the
	// previous implementation).
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "heal op not found")
}

// IsHealVerificationAlreadySubmitted reports whether err corresponds to the
// chain rejecting a duplicate heal-verification submission from the same
// verifier (registered as audittypes.ErrHealVerificationExists, code 15,
// surfaced at msg_storage_truth.go:287).
func IsHealVerificationAlreadySubmitted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, audittypes.ErrHealVerificationExists) {
		return true
	}
	return strings.Contains(err.Error(), "verification already submitted by creator")
}

// IsRecheckEvidenceAlreadySubmitted reports whether err corresponds to the
// chain rejecting a duplicate recheck-evidence submission. Chain wraps
// audittypes.ErrInvalidRecheckEvidence (a generic envelope for ALL recheck
// evidence rejections) with the discriminating phrase "recheck evidence
// already submitted for epoch %d ticket %q by %q" at
// msg_storage_truth.go:90.
//
// Because ErrInvalidRecheckEvidence is generic (covers many distinct rejects),
// we cannot collapse on errors.Is alone — we MUST disambiguate via the
// discriminating phrase. typed-OR-substring is therefore an "AND" only when
// the typed sentinel matches; otherwise we accept substring as the sole
// signal (handles older chain builds and double-wrapped errors).
func IsRecheckEvidenceAlreadySubmitted(err error) bool {
	if err == nil {
		return false
	}
	// Phrase match is required because ErrInvalidRecheckEvidence is a
	// generic envelope for all recheck-evidence rejections (length, signer,
	// hash) — we must disambiguate via the unique already-submitted phrase.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "recheck evidence already submitted")
}

// IsTransientGrpc reports whether err is a transient gRPC failure that
// callers should treat as "retry next tick" rather than "chain reject".
//
// Concretely: codes.Unavailable, codes.DeadlineExceeded, codes.Aborted,
// codes.ResourceExhausted, plus context.Canceled / context.DeadlineExceeded
// at the supernode side. Callers in the heal/verify/recheck paths must
// short-circuit on this BEFORE they classify an error as "chain-rejected,
// safe to clean up local state".
func IsTransientGrpc(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable,
			codes.DeadlineExceeded,
			codes.Aborted,
			codes.ResourceExhausted,
			codes.Canceled:
			return true
		}
	}
	return false
}
