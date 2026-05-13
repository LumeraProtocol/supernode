package self_healing

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

func TestDispatchOpContextForHealOpUsesEpochAnchorDeadlineWhenEarlier(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	h.svc.cfg.DispatchOpTimeout = time.Hour
	h.audit.currentAnchor = audittypes.EpochAnchor{EpochId: 10, EpochEndHeight: 100}
	h.audit.epochAnchors[11] = audittypes.EpochAnchor{EpochId: 11, EpochEndHeight: 101}

	ctx, cancel := h.svc.dispatchOpContextForHealOp(context.Background(), audittypes.HealOp{HealOpId: 1, DeadlineEpochId: 11})
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected derived deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("expected deadline derived from 1 remaining chain block, got %s", remaining)
	}
}

func TestDispatchOpContextForHealOpFallsBackToHardTimeoutWhenAnchorsMissing(t *testing.T) {
	h := newHarness(t, "sn-healer", audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)
	h.svc.cfg.DispatchOpTimeout = 50 * time.Millisecond

	ctx, cancel := h.svc.dispatchOpContextForHealOp(context.Background(), audittypes.HealOp{HealOpId: 1, DeadlineEpochId: 99})
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected hard timeout deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("expected hard timeout fallback, got %s", remaining)
	}
}

func TestPublishStagingDirDeletesClaimBeforeRemovingStagingDir(t *testing.T) {
	src, err := os.ReadFile("finalizer.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	deleteIdx := strings.Index(body, "DeleteHealClaim(ctx, claim.HealOpID)")
	removeIdx := strings.Index(body, "os.RemoveAll(claim.StagingDir)")
	if deleteIdx < 0 || removeIdx < 0 {
		t.Fatalf("expected publishStagingDir cleanup calls to exist")
	}
	if deleteIdx > removeIdx {
		t.Fatalf("publishStagingDir must delete durable claim row before removing staging dir")
	}
}
