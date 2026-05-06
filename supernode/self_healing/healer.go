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

	if _, err := s.lumera.AuditMsg().ClaimHealComplete(ctx, op.HealOpId, op.TicketId, manifestHash, ""); err != nil {
		if isChainHealOpInvalidState(err) {
			if recErr := s.reconcileExistingClaim(ctx, op, manifestHash, stagingDir); recErr != nil {
				_ = os.RemoveAll(stagingDir)
				return fmt.Errorf("submit failed (%v) and reconcile failed: %w", err, recErr)
			}
			return nil
		}
		_ = s.store.DeletePendingHealClaim(ctx, op.HealOpId)
		_ = os.RemoveAll(stagingDir)
		lep6metrics.IncHealClaim("submit_error")
		return fmt.Errorf("submit claim: %w", err)
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

// isChainHealOpInvalidState detects the chain's wrapped
// ErrHealOpInvalidState surface for "status does not accept healer
// completion claim" — meaning the op has already moved past SCHEDULED.
// String-matched because audittypes errors are wrapped and we want to be
// resilient to both go-error chain lookups and any client-side wrapping.
func isChainHealOpInvalidState(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not accept healer completion claim")
}
