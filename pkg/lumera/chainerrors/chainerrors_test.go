package chainerrors

import (
	"context"
	"errors"
	"fmt"
	"testing"

	errorsmod "cosmossdk.io/errors"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// abciErr re-creates an error with typed sentinel preserved across %w wrap,
// matching the production wrap path in pkg/lumera/modules/tx/impl.go after
// the Wave 0 boundary fix.
func abciErr(sentinel *errorsmod.Error, rawLog string) error {
	return fmt.Errorf("tx failed: code=%d codespace=%s height=0 gas_wanted=0 gas_used=0 raw_log=%s: %w",
		sentinel.ABCICode(), sentinel.Codespace(), rawLog, sentinel)
}

func TestIsHealOpInvalidState(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed sentinel", audittypes.ErrHealOpInvalidState, true},
		{"typed sentinel wrapped via fmt", fmt.Errorf("submit claim: %w", audittypes.ErrHealOpInvalidState), true},
		{"production wrap shape", abciErr(audittypes.ErrHealOpInvalidState, "heal op status HEALER_REPORTED does not accept healer completion claim"), true},
		{"substring fallback only", errors.New("rpc: heal op status FAILED does not accept healer completion claim (untyped)"), true},
		{"unrelated error", errors.New("network unreachable"), false},
		{"unrelated typed", audittypes.ErrHealOpNotFound, false},
		// Defensive: must NOT confuse with transient gRPC errors.
		{"transient unavailable", status.Error(codes.Unavailable, "connection lost"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHealOpInvalidState(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsHealOpNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed sentinel", audittypes.ErrHealOpNotFound, true},
		{"typed sentinel wrapped via fmt", fmt.Errorf("get heal op: %w", audittypes.ErrHealOpNotFound), true},
		{"production tx wrap shape", abciErr(audittypes.ErrHealOpNotFound, "heal op 42 not found"), true},
		{"gRPC NotFound from chain query", status.Error(codes.NotFound, "heal op not found"), true},
		// Negative — the previous broad implementation matched these and
		// caused destructive cleanup. The new predicate must NOT.
		{"gRPC NotFound but unrelated message", status.Error(codes.NotFound, "block 12345 not found"), false},
		{"plain string with not_found but no heal op", errors.New("codec: key not_found"), false},
		{"transient unavailable", status.Error(codes.Unavailable, "connection lost"), false},
		{"context canceled", context.Canceled, false},
		{"unrelated error", errors.New("network unreachable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHealOpNotFound(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsHealVerificationAlreadySubmitted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed sentinel", audittypes.ErrHealVerificationExists, true},
		{"typed sentinel wrapped via fmt", fmt.Errorf("submit verification: %w", audittypes.ErrHealVerificationExists), true},
		{"production wrap shape", abciErr(audittypes.ErrHealVerificationExists, "verification already submitted by creator"), true},
		{"substring fallback only", errors.New("verification already submitted by creator (untyped)"), true},
		{"unrelated error", errors.New("rpc unauthorized"), false},
		{"transient unavailable", status.Error(codes.Unavailable, "connection lost"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsHealVerificationAlreadySubmitted(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsRecheckEvidenceAlreadySubmitted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"production wrap with phrase", abciErr(audittypes.ErrInvalidRecheckEvidence, "recheck evidence already submitted for epoch 7 ticket \"abc\" by \"lumera1...\""), true},
		{"substring only", errors.New("recheck evidence already submitted somewhere"), true},
		// Generic ErrInvalidRecheckEvidence WITHOUT the phrase covers many
		// other rejections (length, signer, hash) — must NOT match.
		{"typed sentinel without phrase (generic envelope)", audittypes.ErrInvalidRecheckEvidence, false},
		{"typed sentinel different reject phrase", fmt.Errorf("creator does not match expected: %w", audittypes.ErrInvalidRecheckEvidence), false},
		{"transient unavailable", status.Error(codes.Unavailable, "connection lost"), false},
		{"unrelated error", errors.New("network unreachable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRecheckEvidenceAlreadySubmitted(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsTransientGrpc(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, true},
		{"context deadline", context.DeadlineExceeded, true},
		{"context canceled wrapped", fmt.Errorf("op aborted: %w", context.Canceled), true},
		{"grpc Unavailable", status.Error(codes.Unavailable, "connection lost"), true},
		{"grpc DeadlineExceeded", status.Error(codes.DeadlineExceeded, "rpc timed out"), true},
		{"grpc Aborted", status.Error(codes.Aborted, "tx aborted"), true},
		{"grpc ResourceExhausted", status.Error(codes.ResourceExhausted, "throttled"), true},
		{"grpc Canceled", status.Error(codes.Canceled, "client canceled"), true},
		// Definitely-not-transient cases.
		{"grpc NotFound", status.Error(codes.NotFound, "heal op not found"), false},
		{"grpc InvalidArgument", status.Error(codes.InvalidArgument, "bad input"), false},
		{"typed audit error", audittypes.ErrHealOpInvalidState, false},
		{"plain string", errors.New("network unreachable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientGrpc(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestRegression_TransientNotFoundDoesNotMatchHealOpNotFound is the
// regression test for C4 — the previous isChainHealOpNotFound matched any
// "not found" substring including transient gRPC errors, leading to
// destructive cleanup of healer staging dirs.
func TestRegression_TransientNotFoundDoesNotMatchHealOpNotFound(t *testing.T) {
	transientCases := []error{
		errors.New("rpc error: block 12345 not found at height 7"),
		errors.New("codec: key not_found in store"),
		status.Error(codes.NotFound, "block at height 99 not found"),
	}
	for i, e := range transientCases {
		if IsHealOpNotFound(e) {
			t.Fatalf("case %d: transient %q must NOT classify as heal-op-not-found", i, e)
		}
	}
}

func TestIsHealOpPastDeadline(t *testing.T) {
	deadlineErr := fmt.Errorf("submit claim: %w", errorsmod.Wrap(audittypes.ErrHealOpInvalidState, "heal op deadline has passed"))
	if !IsHealOpPastDeadline(deadlineErr) {
		t.Fatalf("expected deadline invalid-state error to match")
	}
	stateErr := fmt.Errorf("submit claim: %w", errorsmod.Wrap(audittypes.ErrHealOpInvalidState, "heal op status VERIFIED does not accept healer completion claim"))
	if IsHealOpPastDeadline(stateErr) {
		t.Fatalf("generic invalid-state error must not be treated as deadline")
	}
}
