package self_healing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/chainerrors"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

// reconstructAndClaim runs LEP-6 §19 Phase 1 for one heal-op.
//
// Steps:
//
//  1. Acquire semReconstruct (RAM cap; RaptorQ is heavy).
//  2. cascadeService.RecoveryReseed(PersistArtifacts=false, StagingDir=…) —
//     reconstructs the file, verifies hash against Action.DataHash,
//     regenerates RQ artefacts, STAGES to disk. NO KAD publish.
//  3. Submit MsgClaimHealComplete{HealManifestHash} FIRST. Submit-then-
//     persist ordering: if submit fails (mempool, signing, chain-rejected)
//     no SQLite row is left; next tick retries cleanly.
//  4. On chain acceptance, persist (heal_op_id, ticket_id, manifest_hash,
//     staging_dir) to heal_claims_submitted so finalizer can drive the op.
//
// Crash-recovery path: if submit succeeded but persist crashed, the next
// tick's dispatchHealerOps sees the chain has moved past SCHEDULED (or
// the resubmit fails with "does not accept healer completion claim"). We
// reconcile via reconcileExistingClaim — query GetHealOp; if status ∈
// {HEALER_REPORTED, VERIFIED, FAILED, EXPIRED} and ResultHash matches
// the manifest we just rebuilt, persist the dedup row and let finalizer
// take over.
func (s *Service) reconstructAndClaim(ctx context.Context, op audittypes.HealOp) error {
	if err := s.semReconstruct.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.semReconstruct.Release(1)

	stagingDir := filepath.Join(s.cfg.StagingRoot, fmt.Sprintf("%d", op.HealOpId))
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return fmt.Errorf("mkdir staging: %w", err)
	}

	task := s.cascadeFactory.NewCascadeRegistrationTask()
	res, err := task.RecoveryReseed(ctx, &cascadeService.RecoveryReseedRequest{
		ActionID:         op.TicketId,
		PersistArtifacts: false,
		StagingDir:       stagingDir,
	})
	if err != nil {
		// Reconstruction failed (Scenario C). Per LEP-6, healer simply does
		// not submit ClaimHealComplete; chain will EXPIRE the op at deadline.
		// Clean staging dir; nothing to publish.
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("recovery reseed: %w", err)
	}
	if !res.DataHashVerified {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("data hash not verified")
	}
	manifestHash := strings.TrimSpace(res.ReconstructedHashB64)
	if manifestHash == "" {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("empty manifest hash")
	}

	// Pre-stage before chain submit. This closes the restart window where the
	// tx is accepted but the process dies before recording local dedup state;
	// on restart, the pending row prevents a duplicate submit loop and lets
	// finalizer/reconciliation continue from local durable state.
	if err := s.store.RecordPendingHealClaim(ctx, op.HealOpId, op.TicketId, manifestHash, stagingDir); err != nil {
		if errors.Is(err, queries.ErrLEP6ClaimAlreadyRecorded) {
			lep6metrics.IncHealClaim("dedup")
			return nil
		}
		_ = os.RemoveAll(stagingDir)
		lep6metrics.IncHealClaim("stage_error")
		return fmt.Errorf("stage heal claim before submit: %w", err)
	}

	// H1 fix: pre-check deadline before fee-burning submit. RaptorQ +
	// VerifierFetchAttempts × VerifierFetchTimeout can take minutes,
	// during which the deadline epoch may pass. Chain rejects past-
	// deadline submits via ErrHealOpInvalidState, but we'd still pay the
	// gas to find that out — and the dispatcher would retry every poll
	// until the chain status flipped. Save the fee + the staging cleanup
	// loop by checking GetCurrentEpoch first.
	if expired, expErr := s.healOpDeadlinePassed(ctx, op); expErr != nil {
		// Couldn't determine deadline — let the submit attempt proceed
		// (chain will reject if needed). Don't block on a transient
		// query failure.
		logtrace.Warn(ctx, "self_healing(LEP-6): could not check deadline before submit; proceeding", logtrace.Fields{
			"heal_op_id":        op.HealOpId,
			logtrace.FieldError: expErr.Error(),
		})
	} else if expired {
		_ = s.store.DeletePendingHealClaim(ctx, op.HealOpId)
		_ = os.RemoveAll(stagingDir)
		lep6metrics.IncHealClaim("deadline_skipped")
		logtrace.Warn(ctx, "self_healing(LEP-6): heal op deadline passed before submit; skipping", logtrace.Fields{
			"heal_op_id":  op.HealOpId,
			"deadline":    op.DeadlineEpochId,
			"staging_dir": stagingDir,
		})
		return nil
	}

	if _, err := s.lumera.AuditMsg().ClaimHealComplete(ctx, op.HealOpId, op.TicketId, manifestHash, ""); err != nil {
		// Transient gRPC failures (Unavailable / DeadlineExceeded / cancellation)
		// MUST NOT trigger destructive cleanup of staging — Wave 0 fix for C3.
		if chainerrors.IsTransientGrpc(err) {
			lep6metrics.IncHealClaim("submit_transient")
			return fmt.Errorf("submit claim (transient, will retry): %w", err)
		}
		if chainerrors.IsHealOpPastDeadline(err) {
			_ = s.store.DeletePendingHealClaim(ctx, op.HealOpId)
			_ = os.RemoveAll(stagingDir)
			lep6metrics.IncHealClaim("deadline_rejected")
			logtrace.Warn(ctx, "self_healing(LEP-6): chain rejected heal claim after deadline; skipping reconcile", logtrace.Fields{
				"heal_op_id":        op.HealOpId,
				"deadline":          op.DeadlineEpochId,
				logtrace.FieldError: err.Error(),
			})
			return nil
		}
		if chainerrors.IsHealOpInvalidState(err) {
			return s.reconcilePendingClaimSubmitError(ctx, op, err)
		}
		// Matee C3 follow-up: do not destructively drop staging on an
		// unclassified submit error until we query chain state. A tx can be
		// committed while the client receives a non-canonical transport / ABCI
		// wrapper error that is neither IsTransientGrpc nor the typed invalid-
		// state sentinel. resumePendingHealClaim promotes the row when chain
		// shows our manifest, or deletes pending+staging only when chain still
		// has no accepted claim / accepted a different manifest.
		return s.reconcilePendingClaimSubmitError(ctx, op, err)
	}

	if err := s.store.MarkHealClaimSubmitted(ctx, op.HealOpId); err != nil {
		lep6metrics.IncHealClaim("mark_error")
		return fmt.Errorf("mark heal claim submitted (chain accepted): %w", err)
	}
	lep6metrics.IncHealClaim("submitted")
	logtrace.Info(ctx, "self_healing(LEP-6): claim submitted", logtrace.Fields{
		"heal_op_id":  op.HealOpId,
		"ticket_id":   op.TicketId,
		"manifest_h":  manifestHash,
		"staging_dir": stagingDir,
	})
	return nil
}

func (s *Service) reconcilePendingClaimSubmitError(ctx context.Context, op audittypes.HealOp, submitErr error) error {
	if recErr := s.resumePendingHealClaim(ctx, op); recErr != nil {
		return fmt.Errorf("submit failed (%v) and pending reconcile failed: %w", submitErr, recErr)
	}
	hasSubmitted, err := s.store.HasHealClaim(ctx, op.HealOpId)
	if err != nil {
		return fmt.Errorf("submit failed (%v) and post-reconcile submitted lookup failed: %w", submitErr, err)
	}
	if hasSubmitted {
		return nil
	}
	lep6metrics.IncHealClaim("submit_error")
	return fmt.Errorf("submit claim: %w", submitErr)
}

// reconcileExistingClaim handles the post-crash case where the chain has
// advanced past SCHEDULED (i.e. our prior submit was accepted but we lost
// the response or crashed before persisting). We re-fetch the op, confirm
// the recorded ResultHash matches the manifest we just rebuilt, and then
// persist the dedup row so the finalizer takes over.
//
// If the chain ResultHash differs, the staged data is irrelevant (a
// previous run produced different bytes — file changed underneath, or
// non-determinism slipped in). Drop staging, do nothing — let the heal-op
// run its course on chain.
func (s *Service) reconcileExistingClaim(ctx context.Context, op audittypes.HealOp, manifestHash, stagingDir string) error {
	resp, err := s.lumera.Audit().GetHealOp(ctx, op.HealOpId)
	if err != nil {
		return fmt.Errorf("get heal op: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("nil heal op response")
	}
	chainOp := resp.HealOp
	if chainOp.ResultHash != manifestHash {
		// Different manifest on chain → our staged bytes don't match what
		// chain expects. Discard staging and let the existing chain op
		// finish without our involvement.
		logtrace.Warn(ctx, "self_healing(LEP-6): chain ResultHash differs from current manifest; abandoning staging", logtrace.Fields{
			"heal_op_id":   op.HealOpId,
			"chain_hash":   chainOp.ResultHash,
			"current_hash": manifestHash,
			"staging_dir":  stagingDir,
			"chain_status": chainOp.Status.String(),
		})
		_ = os.RemoveAll(stagingDir)
		return nil
	}
	// Manifest matches — persist/mark dedup row so finalizer can publish on
	// VERIFIED. If this tick pre-staged the row before seeing the already-on-
	// chain error, mark it submitted; otherwise insert a submitted row.
	if err := s.store.RecordHealClaim(ctx, op.HealOpId, op.TicketId, manifestHash, stagingDir); err != nil {
		if errors.Is(err, queries.ErrLEP6ClaimAlreadyRecorded) {
			if markErr := s.store.MarkHealClaimSubmitted(ctx, op.HealOpId); markErr != nil {
				return fmt.Errorf("mark reconciled claim submitted: %w", markErr)
			}
		} else {
			return fmt.Errorf("record reconciled claim: %w", err)
		}
	}
	logtrace.Info(ctx, "self_healing(LEP-6): reconciled existing chain claim", logtrace.Fields{
		"heal_op_id":   op.HealOpId,
		"chain_status": chainOp.Status.String(),
		"manifest_h":   manifestHash,
	})
	lep6metrics.IncHealClaimReconciled()
	lep6metrics.IncHealClaim("reconciled")
	return nil
}

// (Wave 0): isChainHealOpInvalidState helper removed; classification is now
// done via pkg/lumera/chainerrors.IsHealOpInvalidState which uses typed
// sentinel matching (audittypes.ErrHealOpInvalidState) with substring
// fallback, plus an IsTransientGrpc short-circuit at the call site to
// preserve staging on transient gRPC failures.

// healOpDeadlinePassed reports whether op.DeadlineEpochId is at or before
// the current chain epoch. Used by healer/verifier to short-circuit
// fee-burning submits the chain would reject (H1 fix). Returns
// (false, err) if current-epoch query fails so the caller can decide
// whether to proceed (we choose to proceed-and-let-chain-reject for
// transient query failures rather than skip a still-valid op).
func (s *Service) healOpDeadlinePassed(ctx context.Context, op audittypes.HealOp) (bool, error) {
	if op.DeadlineEpochId == 0 {
		// Spec says 0 means "no deadline configured"; chain auto-fills
		// to current+heal_deadline_epochs. If we see 0 here, don't
		// pre-skip.
		return false, nil
	}
	queryCtx, cancel := s.auditQueryContext(ctx)
	defer cancel()
	resp, err := s.lumera.Audit().GetCurrentEpoch(queryCtx)
	if err != nil {
		return false, err
	}
	if resp == nil || resp.EpochId == 0 {
		return false, nil
	}
	return resp.EpochId >= op.DeadlineEpochId, nil
}

// resumePendingHealClaim is the C5 fix: a `pending` claim row from a
// previous tick (crashed between RecordPendingHealClaim and chain ack)
// exists locally. We must reconcile against the chain BEFORE either
// resubmitting (waste) or skipping (data loss).
//
// Decision tree:
//
//   - Chain advanced (HEALER_REPORTED+) and op.ResultHash matches the
//     pending row's manifest_hash → our submit was actually accepted;
//     promote pending → submitted; finalizer takes over. Staging dir
//     is preserved (finalizer reads it).
//
//   - Chain advanced (HEALER_REPORTED+) but op.ResultHash differs → some
//     other healer claim was accepted; our staged bytes are irrelevant.
//     Delete pending row + remove staging.
//
//   - Chain still SCHEDULED → our prior submit was rejected/lost without
//     acceptance. Delete pending row + remove staging so the next
//     dispatch tick attempts a fresh reconstruct (chain has no record).
//
//   - Chain in any final state (FAILED/EXPIRED/VERIFIED with different
//     hash) → cleanup pending row + staging.
//
// Transient gRPC errors during the GetHealOp query do NOT delete state.
func (s *Service) resumePendingHealClaim(ctx context.Context, op audittypes.HealOp) error {
	row, err := s.store.GetHealClaim(ctx, op.HealOpId)
	if err != nil {
		return fmt.Errorf("get pending claim row: %w", err)
	}
	if row.Status != "pending" {
		// Race: another goroutine promoted/deleted the row already.
		return nil
	}
	resp, err := s.lumera.Audit().GetHealOp(ctx, op.HealOpId)
	if err != nil {
		if chainerrors.IsTransientGrpc(err) {
			return fmt.Errorf("get heal op (transient, will retry): %w", err)
		}
		// Non-transient query failure — keep pending row in place;
		// next tick retries.
		return fmt.Errorf("get heal op: %w", err)
	}
	if resp == nil || resp.HealOp.HealOpId == 0 {
		return fmt.Errorf("nil/empty heal op response")
	}
	chainOp := resp.HealOp
	switch chainOp.Status {
	case audittypes.HealOpStatus_HEAL_OP_STATUS_SCHEDULED:
		// Chain has no claim from us; our prior submit was rejected
		// or lost. Drop pending row + staging; let the next tick
		// re-dispatch fresh.
		_ = os.RemoveAll(row.StagingDir)
		if err := s.store.DeletePendingHealClaim(ctx, op.HealOpId); err != nil {
			return fmt.Errorf("delete pending claim after SCHEDULED reconcile: %w", err)
		}
		lep6metrics.IncHealClaim("resume_reset")
		logtrace.Info(ctx, "self_healing(LEP-6): resume reset (chain still SCHEDULED, dropping stale pending)", logtrace.Fields{
			"heal_op_id":   op.HealOpId,
			"staging_dir":  row.StagingDir,
			"chain_status": chainOp.Status.String(),
		})
		return nil
	case audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED:
		if chainOp.ResultHash == row.ManifestHash {
			// Our submit was actually accepted — promote pending → submitted.
			if err := s.store.MarkHealClaimSubmitted(ctx, op.HealOpId); err != nil {
				return fmt.Errorf("mark heal claim submitted (resume): %w", err)
			}
			lep6metrics.IncHealClaimReconciled()
			lep6metrics.IncHealClaim("resume_promoted")
			logtrace.Info(ctx, "self_healing(LEP-6): resume promoted pending → submitted", logtrace.Fields{
				"heal_op_id":   op.HealOpId,
				"chain_status": chainOp.Status.String(),
				"manifest_h":   row.ManifestHash,
			})
			return nil
		}
		// Different healer's claim was accepted — drop our staging.
		_ = os.RemoveAll(row.StagingDir)
		if err := s.store.DeletePendingHealClaim(ctx, op.HealOpId); err != nil {
			return fmt.Errorf("delete pending claim after foreign-hash reconcile: %w", err)
		}
		lep6metrics.IncHealClaim("resume_foreign")
		logtrace.Warn(ctx, "self_healing(LEP-6): resume foreign-hash (different healer's claim accepted)", logtrace.Fields{
			"heal_op_id":   op.HealOpId,
			"chain_hash":   chainOp.ResultHash,
			"pending_hash": row.ManifestHash,
			"chain_status": chainOp.Status.String(),
			"staging_dir":  row.StagingDir,
		})
		return nil
	default:
		// FAILED / EXPIRED / IN_PROGRESS / others — staging is no
		// longer useful; let finalizer drain anything else.
		_ = os.RemoveAll(row.StagingDir)
		if err := s.store.DeletePendingHealClaim(ctx, op.HealOpId); err != nil {
			return fmt.Errorf("delete pending claim after terminal reconcile: %w", err)
		}
		lep6metrics.IncHealClaim("resume_terminal")
		return nil
	}
}
