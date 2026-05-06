package self_healing

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/chainerrors"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"lukechampine.com/blake3"
)

// verifyAndSubmit runs LEP-6 §19 Phase 2 for one heal-op.
//
// Critical correctness rules
//
//  1. The verifier MUST fetch from the assigned healer (op.HealerSupernode
//     Account), not from KAD. KAD is empty during HEALER_REPORTED — the
//     healer publishes only after VERIFIED — so reading from KAD would
//     loop on miss. More importantly, the §19 healer-served path is the
//     only authority before chain quorum.
//
//  2. The verifier MUST compare its computed hash against op.ResultHash
//     (set by the chain from the healer's HealManifestHash), NOT against
//     Action.DataHash. The chain enforces this at
//     lumera/x/audit/v1/keeper/msg_storage_truth.go:291. A verifier that
//     submits VerificationHash != op.ResultHash with verified=true is
//     rejected by the chain. Pinned by TestVerifier_ReadsOpResultHashForComparison.
//
//  3. On fetch failure after VerifierFetchAttempts retries the verifier
//     submits verified=false. The chain rejects empty VerificationHash even
//     for negatives (msg_storage_truth.go:271-273), so we synthesize a
//     non-empty deterministic placeholder hash — for negative attestations
//     the chain only validates equality when `req.Verified == true`
//     (msg_storage_truth.go:288-294), so any non-empty value is accepted.
//
//  4. Persist-AFTER-submit ordering: SQLite dedup row is written ONLY after
//     the chain accepted the tx. A failed submit therefore leaves no row,
//     letting the next tick retry. Reverse ordering would strand the op
//     forever on flaky submits.
func (s *Service) verifyAndSubmit(ctx context.Context, op audittypes.HealOp) error {
	if err := s.semVerify.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.semVerify.Release(1)

	expectedHash := strings.TrimSpace(op.ResultHash)
	if expectedHash == "" {
		return fmt.Errorf("op.ResultHash empty (op not in HEALER_REPORTED?)")
	}

	// H1 fix: pre-check deadline before fetch + submit. Same fee-burn
	// concern as the healer path; chain rejects past-deadline submits
	// via ErrHealOpInvalidState.
	if expired, expErr := s.healOpDeadlinePassed(ctx, op); expErr != nil {
		logtrace.Warn(ctx, "self_healing(LEP-6): could not check verifier deadline; proceeding", logtrace.Fields{
			"heal_op_id":        op.HealOpId,
			logtrace.FieldError: expErr.Error(),
		})
	} else if expired {
		logtrace.Warn(ctx, "self_healing(LEP-6): heal op deadline passed before verifier submit; skipping", logtrace.Fields{
			"heal_op_id": op.HealOpId,
			"deadline":   op.DeadlineEpochId,
		})
		return nil
	}

	bytesGot, fetchErr := s.fetchFromHealerWithRetry(ctx, op)
	if fetchErr != nil {
		// Submit negative verification with a non-empty placeholder hash —
		// chain rejects empty VerificationHash even for negative votes.
		// L2 fix: hash a canonical reason taxonomy instead of the raw
		// fetchErr string so audit trails can correlate identical failure
		// modes across verifiers.
		details := negativeReasonFetchFailed + ":" + fetchErr.Error()
		if err := s.submitNegativeWithReason(ctx, op.HealOpId, negativeReasonFetchFailed, details); err != nil {
			return fmt.Errorf("fetch %v; submit-negative %w", fetchErr, err)
		}
		logtrace.Warn(ctx, "self_healing(LEP-6): verifier submitted negative due to fetch failure", logtrace.Fields{
			"heal_op_id":        op.HealOpId,
			logtrace.FieldError: fetchErr.Error(),
		})
		return nil
	}

	computedHash, hashErr := cascadekit.ComputeBlake3DataHashB64(bytesGot)
	if hashErr != nil {
		details := negativeReasonHashCompute + ":" + hashErr.Error()
		if err := s.submitNegativeWithReason(ctx, op.HealOpId, negativeReasonHashCompute, details); err != nil {
			return fmt.Errorf("hash %v; submit-negative %w", hashErr, err)
		}
		return nil
	}
	verified := computedHash == expectedHash
	details := ""
	if !verified {
		details = negativeReasonHashMismatch
	}
	// Positive: chain validates VerificationHash == op.ResultHash. Negative:
	// chain accepts any non-empty hash. Send computedHash either way so audit
	// trails always carry the verifier's own observation.
	if err := s.submitVerification(ctx, op.HealOpId, verified, computedHash, details); err != nil {
		return fmt.Errorf("submit verification: %w", err)
	}
	logtrace.Info(ctx, "self_healing(LEP-6): verification submitted", logtrace.Fields{
		"heal_op_id":   op.HealOpId,
		"verified":     verified,
		"expected_h":   expectedHash,
		"computed_h":   computedHash,
		"bytes_length": len(bytesGot),
	})
	return nil
}

// L2 fix: small canonical reason taxonomy for negative attestations.
// Hashing the canonical reason (instead of raw error text) means two
// verifiers that observed the same failure class produce the same
// negative-attestation hash — easier audit-trail correlation.
const (
	negativeReasonFetchFailed  = "reason_fetch_failed"
	negativeReasonHashCompute  = "reason_hash_compute_failed"
	negativeReasonHashMismatch = "reason_hash_mismatch"
	negativeReasonOther        = "reason_other"
)

// submitNegativeWithReason synthesizes a deterministic non-empty placeholder
// hash from the canonical reason category and submits a negative
// verification. Chain only validates VerificationHash content for positive
// votes (msg_storage_truth.go:288-294), so any non-empty value is well-
// formed. `details` carries the full free-form context (raw error / file
// info) for the chain log; the on-chain hash is derived from the canonical
// reason only.
func (s *Service) submitNegativeWithReason(ctx context.Context, healOpID uint64, reasonCategory, details string) error {
	if reasonCategory == "" {
		reasonCategory = negativeReasonOther
	}
	placeholder := negativeAttestationHash(reasonCategory)
	return s.submitVerification(ctx, healOpID, false, placeholder, details)
}

// negativeAttestationHash returns a stable non-empty BLAKE3/base64 hash
// derived from the canonical reason category. Format remains a 32-byte
// digest encoded as base64 so downstream consumers don't have to special-
// case width. Wave 2 / L2 fix: input is now a small enum string instead of
// raw fetchErr.Error() — verifiers observing the same failure class
// produce the same negative hash, allowing chain-side correlation.
func negativeAttestationHash(reasonCategory string) string {
	sum := blake3.Sum256([]byte("lep6:negative-attestation:" + reasonCategory))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// submitVerification pre-stages the SQLite dedup row before submitting
// MsgSubmitHealVerification, then marks it submitted after chain acceptance.
// This closes the submit-success/persist-crash window without weakening
// chain authority: on hard tx failure we remove only the pending row so the
// verifier can retry later.
func (s *Service) submitVerification(ctx context.Context, healOpID uint64, verified bool, hash, details string) error {
	if err := s.store.RecordPendingHealVerification(ctx, healOpID, s.identity, verified, hash); err != nil {
		if errors.Is(err, queries.ErrLEP6VerificationAlreadyRecorded) {
			lep6metrics.IncHealVerification("dedup", verified)
			lep6metrics.IncHealVerificationAlreadyExists()
			return nil
		}
		lep6metrics.IncHealVerification("stage_error", verified)
		return fmt.Errorf("stage heal verification before submit: %w", err)
	}

	resp, err := s.lumera.AuditMsg().SubmitHealVerification(ctx, healOpID, verified, hash, details)
	if err != nil {
		// Transient gRPC failures MUST NOT delete the pending row — the
		// next tick will retry and reach idempotent dedup on the chain.
		if chainerrors.IsTransientGrpc(err) {
			lep6metrics.IncHealVerification("submit_transient", verified)
			return fmt.Errorf("submit verification (transient, will retry): %w", err)
		}
		if chainerrors.IsHealVerificationAlreadySubmitted(err) {
			if markErr := s.store.MarkHealVerificationSubmitted(ctx, healOpID, s.identity); markErr != nil {
				return fmt.Errorf("mark reconciled verification submitted: %w", markErr)
			}
			return nil
		}
		_ = s.store.DeletePendingHealVerification(ctx, healOpID, s.identity)
		lep6metrics.IncHealVerification("submit_error", verified)
		return err
	}
	_ = resp
	if err := s.store.MarkHealVerificationSubmitted(ctx, healOpID, s.identity); err != nil {
		lep6metrics.IncHealVerification("mark_error", verified)
		return fmt.Errorf("mark heal verification submitted: %w", err)
	}
	lep6metrics.IncHealVerification("submitted", verified)
	return nil
}

// (Wave 0): isChainVerificationAlreadyExists helper removed; classification
// is centralised in pkg/lumera/chainerrors.IsHealVerificationAlreadySubmitted
// which uses typed sentinel matching (audittypes.ErrHealVerificationExists)
// with substring fallback, and the call site short-circuits on
// IsTransientGrpc to preserve the pending row across transient failures.

// fetchFromHealerWithRetry is the §19 healer-served-path GET with bounded
// exponential backoff. Returns the reconstructed file bytes (concatenated
// from chunks if chunked).
func (s *Service) fetchFromHealerWithRetry(ctx context.Context, op audittypes.HealOp) ([]byte, error) {
	if s.fetcher == nil {
		return nil, fmt.Errorf("verifier fetcher is nil")
	}
	var lastErr error
	for attempt := 0; attempt < s.cfg.VerifierFetchAttempts; attempt++ {
		fetchCtx, cancel := context.WithTimeout(ctx, s.cfg.VerifierFetchTimeout)
		bytesGot, err := s.fetcher.FetchReconstructed(fetchCtx, op.HealOpId, op.HealerSupernodeAccount, s.identity)
		cancel()
		if err == nil {
			return bytesGot, nil
		}
		lastErr = err
		if attempt+1 < s.cfg.VerifierFetchAttempts {
			delay := s.cfg.VerifierBackoffBase * (1 << attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, lastErr
}
