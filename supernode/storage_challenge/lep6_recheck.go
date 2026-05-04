package storage_challenge

import (
	"context"
	"fmt"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/supernode/recheck"
)

// Recheck executes a LEP-6 RECHECK-bucket proof for the candidate and returns
// the result shape expected by MsgSubmitStorageRecheckEvidence. It reuses the
// same deterministic compound-proof machinery as the epoch dispatcher, but
// writes into a temporary buffer so recheck results are never mixed into the
// host_reporter epoch-report buffer.
func (d *LEP6Dispatcher) Recheck(ctx context.Context, c recheck.Candidate) (recheck.RecheckResult, error) {
	if !c.Valid() {
		return recheck.RecheckResult{}, fmt.Errorf("invalid recheck candidate")
	}
	paramsResp, err := d.client.Audit().GetParams(ctx)
	if err != nil {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: get params: %w", err)
	}
	if paramsResp == nil {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: get params returned nil response")
	}
	params := paramsResp.Params
	if params.StorageTruthEnforcementMode == audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: enforcement mode unspecified")
	}
	anchorResp, err := d.client.Audit().GetEpochAnchor(ctx, c.EpochID)
	if err != nil {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: get epoch anchor %d: %w", c.EpochID, err)
	}
	if anchorResp == nil {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: epoch anchor not yet available for epoch %d", c.EpochID)
	}
	if anchorResp.Anchor.EpochId != c.EpochID {
		return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: epoch anchor not yet available for epoch %d", c.EpochID)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	orig := d.buffer
	tmp := NewBuffer()
	d.buffer = tmp
	defer func() { d.buffer = orig }()

	if err := d.dispatchTicket(ctx, c.EpochID, anchorResp.Anchor, params, c.TargetAccount, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECHECK, c.TicketID); err != nil {
		return recheck.RecheckResult{}, err
	}
	results := tmp.CollectResults(c.EpochID)
	for _, r := range results {
		if r == nil || r.TicketId != c.TicketID || r.TargetSupernodeAccount != c.TargetAccount {
			continue
		}
		cls := r.ResultClass
		if cls == audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH {
			cls = audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_RECHECK_CONFIRMED_FAIL
		}
		return recheck.RecheckResult{TranscriptHash: r.TranscriptHash, ResultClass: cls, Details: r.Details}, nil
	}
	return recheck.RecheckResult{}, fmt.Errorf("lep6 recheck: no result emitted for epoch=%d ticket=%s target=%s", c.EpochID, c.TicketID, c.TargetAccount)
}
