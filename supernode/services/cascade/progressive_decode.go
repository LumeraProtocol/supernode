package cascade

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
)

// retrieveAndDecodeProgressively performs a minimal two-step retrieval for a single-block layout:
// 1) Fetch approximately requiredSymbolPercent of symbols and try decoding.
// 2) If that fails, fetch all available symbols from the block and try again.
// This replaces earlier multi-block balancing and multi-threshold escalation.
func (task *CascadeRegistrationTask) retrieveAndDecodeProgressively(
	ctx context.Context,
	layout codec.Layout,
	actionID string,
	fields logtrace.Fields,
) (adaptors.DecodeResponse, error) {
	// Ensure base context fields are present for logs
	if fields == nil {
		fields = logtrace.Fields{}
	}
	fields[logtrace.FieldActionID] = actionID

	if len(layout.Blocks) == 0 {
		return adaptors.DecodeResponse{}, fmt.Errorf("empty layout: no blocks")
	}

	// Single-block fast path
	if len(layout.Blocks) == 1 {
		blk := layout.Blocks[0]
		total := len(blk.Symbols)
		if total == 0 {
			return adaptors.DecodeResponse{}, fmt.Errorf("empty layout: no symbols")
		}

		// Step 1: try with requiredSymbolPercent of symbols
		reqCount := (total*requiredSymbolPercent + 99) / 100
		if reqCount < 1 {
			reqCount = 1
		}
		if reqCount > total {
			reqCount = total
		}
		fields["targetPercent"] = requiredSymbolPercent
		fields["targetCount"] = reqCount
		logtrace.Info(ctx, "retrieving initial symbols (single block)", fields)

		keys := blk.Symbols[:reqCount]
		symbols, err := task.P2PClient.BatchRetrieve(ctx, keys, reqCount, actionID)
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "failed to retrieve symbols", fields)
			return adaptors.DecodeResponse{}, fmt.Errorf("failed to retrieve symbols: %w", err)
		}

		decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{
			ActionID: actionID,
			Symbols:  symbols,
			Layout:   layout,
		})
		if err == nil {
			return decodeInfo, nil
		}

		// Step 2: escalate to all symbols
		logtrace.Info(ctx, "initial decode failed; retrieving all symbols (single block)", nil)
		symbols, err = task.P2PClient.BatchRetrieve(ctx, blk.Symbols, total, actionID)
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "failed to retrieve all symbols", fields)
			return adaptors.DecodeResponse{}, fmt.Errorf("failed to retrieve symbols: %w", err)
		}
		return task.RQ.Decode(ctx, adaptors.DecodeRequest{
			ActionID: actionID,
			Symbols:  symbols,
			Layout:   layout,
		})
	}

	// Multi-block layouts are not supported by current policy
	return adaptors.DecodeResponse{}, fmt.Errorf("unsupported layout: expected 1 block, found %d", len(layout.Blocks))
}
