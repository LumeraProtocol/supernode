package cascade

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
)

// retrieveAndDecodeProgressively performs a minimal two-step retrieval for a single-block layout:
// 1) Send ALL keys with a minimum required count (requiredSymbolPercent).
// 2) If decode fails, escalate by asking for ALL symbols (required = total).
func (task *CascadeRegistrationTask) retrieveAndDecodeProgressively(ctx context.Context, layout codec.Layout, actionID string,
	fields logtrace.Fields) (adaptors.DecodeResponse, error) {
	if fields == nil {
		fields = logtrace.Fields{}
	}
	fields[logtrace.FieldActionID] = actionID

	if len(layout.Blocks) == 0 {
		return adaptors.DecodeResponse{}, fmt.Errorf("empty layout: no blocks")
	}

	// Single-block path
	if len(layout.Blocks) == 1 {
		blk := layout.Blocks[0]
		total := len(blk.Symbols)
		if total == 0 {
			return adaptors.DecodeResponse{}, fmt.Errorf("empty layout: no symbols")
		}

		// Step 1: send ALL keys, require only reqCount
		reqCount := (total*requiredSymbolPercent + 99) / 100
		if reqCount < 1 {
			reqCount = 1
		} else if reqCount > total {
			reqCount = total
		}
		fields["targetPercent"] = requiredSymbolPercent
		fields["targetCount"] = reqCount
		fields["total"] = total
		logtrace.Info(ctx, "retrieving initial symbols (single block)", fields)

		symbols, err := task.P2PClient.BatchRetrieve(ctx, blk.Symbols, reqCount, actionID)
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

		// Step 2: escalate to require ALL symbols
		fields["escalating"] = true
		fields["requiredCount"] = total
		logtrace.Info(ctx, "initial decode failed; retrieving all symbols (single block)", fields)

		symbols, err = task.P2PClient.BatchRetrieve(ctx, blk.Symbols, reqCount*2, actionID)
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

	return adaptors.DecodeResponse{}, fmt.Errorf("unsupported layout: expected 1 block, found %d", len(layout.Blocks))
}
