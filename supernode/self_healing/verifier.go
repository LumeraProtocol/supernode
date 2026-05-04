package self_healing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
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

	bytesGot, fetchErr := s.fetchFromHealerWithRetry(ctx, op)
	if fetchErr != nil {
		// Submit negative verification with a non-empty placeholder hash —
		// chain rejects empty VerificationHash even for negative votes.
		details := fmt.Sprintf("fetch_failed:%v", fetchErr)
		if err := s.submitNegativeWithReason(ctx, op.HealOpId, details); err != nil {
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
		details := fmt.Sprintf("hash_compute_failed:%v", hashErr)
		if err := s.submitNegativeWithReason(ctx, op.HealOpId, details); err != nil {
			return fmt.Errorf("hash %v; submit-negative %w", hashErr, err)
		}
		return nil
	}
	verified := computedHash == expectedHash
	details := ""
	if !verified {
		details = "hash_mismatch"
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

// submitNegativeWithReason synthesizes a deterministic non-empty placeholder
// hash from the failure reason and submits a negative verification. Chain
// only validates VerificationHash content for positive votes
// (msg_storage_truth.go:288-294), so any non-empty value is well-formed.
func (s *Service) submitNegativeWithReason(ctx context.Context, healOpID uint64, reason string) error {
	placeholder := negativeAttestationHash(reason)
	return s.submitVerification(ctx, healOpID, false, placeholder, reason)
}

// negativeAttestationHash returns a stable non-empty base64 hash derived
// from `reason` so audit trails can correlate identical failure modes.
// Format matches the action.DataHash recipe (32-byte digest, base64) so
// downstream consumers don't have to special-case width.
func negativeAttestationHash(reason string) string {
	sum := sha256.Sum256([]byte("lep6:negative-attestation:" + reason))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// submitVerification submits MsgSubmitHealVerification THEN persists the
// SQLite dedup row only on successful chain acceptance.
//
// Idempotency on retry: if the chain has already recorded a verification
// from this verifier (for instance, a previous tick's submit succeeded but
// the supernode crashed before persisting), it returns ErrHealVerification
// Exists. We treat that as success and persist the row so the next tick
// stops retrying.
func (s *Service) submitVerification(ctx context.Context, healOpID uint64, verified bool, hash, details string) error {
	resp, err := s.lumera.AuditMsg().SubmitHealVerification(ctx, healOpID, verified, hash, details)
	if err != nil {
		// If the chain already has a verification from us (prior submit
		// succeeded but persist crashed), reconcile by persisting the
		// dedup row now.
		if isChainVerificationAlreadyExists(err) {
			if persistErr := s.store.RecordHealVerification(ctx, healOpID, s.identity, verified, hash); persistErr != nil && !errors.Is(persistErr, queries.ErrLEP6VerificationAlreadyRecorded) {
				return fmt.Errorf("reconcile dedup row: %w", persistErr)
			}
			return nil
		}
		return err
	}
	_ = resp
	// Chain accepted — persist for restart dedup. If row already exists
	// (in-flight retry beat us), it's a no-op.
	if err := s.store.RecordHealVerification(ctx, healOpID, s.identity, verified, hash); err != nil {
		if errors.Is(err, queries.ErrLEP6VerificationAlreadyRecorded) {
			return nil
		}
		return fmt.Errorf("record heal verification: %w", err)
	}
	return nil
}

// isChainVerificationAlreadyExists detects the chain's
// ErrHealVerificationExists wrapped string. We can't import the chain's
// errors package here without cycling through audittypes, but the wrapped
// message is stable.
func isChainVerificationAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "verification already submitted by creator")
}

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
