package self_healing

import (
	"context"
	"fmt"
	"os"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
)

// finalizeClaim runs LEP-6 §19 Phase 3 for one persisted heal-op claim.
//
// Possible chain states for a claim row whose heal_op_id is queried:
//   - SCHEDULED / IN_PROGRESS — chain has not yet recorded the healer's
//     claim. Treat as transient; do nothing this tick.
//   - HEALER_REPORTED — claim recorded but quorum not yet reached. No-op.
//   - VERIFIED — quorum reached; publish staging dir to KAD via
//     cascadeService.PublishStagedArtefacts, then delete the dir + the
//     dedup row.
//   - FAILED — verifiers rejected the claim or the chain finalized
//     negatively. Delete staging dir + dedup row; do NOT publish (Scenario
//     B). Chain has already applied §20 penalties.
//   - EXPIRED — deadline passed before quorum (Scenario C, late-detected).
//     Same handling as FAILED on the supernode side.
//   - GetHealOp errors with not-found — treat as EXPIRED (chain may have
//     pruned), delete staging.
func (s *Service) finalizeClaim(ctx context.Context, claim queries.HealClaimRecord) error {
	resp, err := s.lumera.Audit().GetHealOp(ctx, claim.HealOpID)
	if err != nil {
		if isChainHealOpNotFound(err) {
			logtrace.Warn(ctx, "self_healing(LEP-6): heal-op not found on chain; cleaning abandoned claim", logtrace.Fields{
				logtrace.FieldError: err.Error(),
				"heal_op_id":        claim.HealOpID,
				"staging_dir":       claim.StagingDir,
			})
			return s.cleanupClaim(ctx, claim, audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED)
		}
		// Defensive: don't blow away local state on transient query errors.
		// A persistent error is logged by the caller; row will be retried
		// next tick.
		return fmt.Errorf("get heal op: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("nil heal op response")
	}
	op := resp.HealOp
	switch op.Status {
	case audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED:
		return s.publishStagingDir(ctx, claim)
	case audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED:
		return s.cleanupClaim(ctx, claim, op.Status)
	default:
		// SCHEDULED / IN_PROGRESS / HEALER_REPORTED — quorum pending.
		return nil
	}
}

func (s *Service) publishStagingDir(ctx context.Context, claim queries.HealClaimRecord) error {
	if err := s.semPublish.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.semPublish.Release(1)

	task := s.cascadeFactory.NewCascadeRegistrationTask()
	if err := task.PublishStagedArtefacts(ctx, claim.StagingDir); err != nil {
		// Leave row + staging in place; next tick retries publish. Chain
		// has already recorded VERIFIED so no on-chain work pending.
		return fmt.Errorf("publish staged artefacts: %w", err)
	}
	if err := os.RemoveAll(claim.StagingDir); err != nil {
		logtrace.Warn(ctx, "self_healing(LEP-6): staging cleanup after publish failed", logtrace.Fields{
			logtrace.FieldError: err.Error(),
			"heal_op_id":        claim.HealOpID,
			"staging_dir":       claim.StagingDir,
		})
	}
	if err := s.store.DeleteHealClaim(ctx, claim.HealOpID); err != nil {
		return fmt.Errorf("delete heal claim row: %w", err)
	}
	lep6metrics.IncHealFinalizePublish()
	logtrace.Info(ctx, "self_healing(LEP-6): published staged artefacts to KAD", logtrace.Fields{
		"heal_op_id":  claim.HealOpID,
		"ticket_id":   claim.TicketID,
		"staging_dir": claim.StagingDir,
	})
	return nil
}

func (s *Service) cleanupClaim(ctx context.Context, claim queries.HealClaimRecord, status audittypes.HealOpStatus) error {
	if err := os.RemoveAll(claim.StagingDir); err != nil {
		logtrace.Warn(ctx, "self_healing(LEP-6): staging cleanup failed", logtrace.Fields{
			logtrace.FieldError: err.Error(),
			"heal_op_id":        claim.HealOpID,
			"status":            status.String(),
		})
	}
	if err := s.store.DeleteHealClaim(ctx, claim.HealOpID); err != nil {
		return fmt.Errorf("delete heal claim row: %w", err)
	}
	lep6metrics.IncHealFinalizeCleanup(status.String())
	logtrace.Info(ctx, "self_healing(LEP-6): claim cleaned up (no publish)", logtrace.Fields{
		"heal_op_id": claim.HealOpID,
		"status":     status.String(),
	})
	return nil
}

func isChainHealOpNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not_found")
}
