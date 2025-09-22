package cascade

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
)

// retrieveAndDecodeProgressively performs a minimal two-step retrieval for a single-block layout:
// 1) Send ALL keys with a minimum required count (requiredSymbolPercent).
// 2) If decode fails, escalate by asking for ALL symbols (required = total).
func (task *CascadeRegistrationTask) retrieveAndDecodeProgressively(ctx context.Context, layout codec.Layout, actionID string,
	fields logtrace.Fields) (adaptors.DecodeResponse, p2p.BatchRetrieveStats, error) {
	if fields == nil {
		fields = logtrace.Fields{}
	}
	fields[logtrace.FieldActionID] = actionID

	if len(layout.Blocks) == 0 {
		return adaptors.DecodeResponse{}, p2p.BatchRetrieveStats{}, fmt.Errorf("empty layout: no blocks")
	}

	// Single-block path
	if len(layout.Blocks) == 1 {
		blk := layout.Blocks[0]
		total := len(blk.Symbols)
		if total == 0 {
			return adaptors.DecodeResponse{}, p2p.BatchRetrieveStats{}, fmt.Errorf("empty layout: no symbols")
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

		stats := p2p.BatchRetrieveStats{}
		symbols, attemptStats, err := task.P2PClient.BatchRetrieve(ctx, blk.Symbols, reqCount, actionID)
		stats = mergeRetrieveStats(stats, attemptStats)
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "failed to retrieve symbols", fields)
			return adaptors.DecodeResponse{}, stats, fmt.Errorf("failed to retrieve symbols: %w", err)
		}

		decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{
			ActionID: actionID,
			Symbols:  symbols,
			Layout:   layout,
		})
		if err == nil {
			return decodeInfo, stats, nil
		}

		// Step 2: escalate to require ALL symbols
		fields["escalating"] = true
		fields["requiredCount"] = total
		logtrace.Info(ctx, "initial decode failed; retrieving all symbols (single block)", fields)

		symbols, attemptStats, err = task.P2PClient.BatchRetrieve(ctx, blk.Symbols, total, actionID)
		stats = mergeRetrieveStats(stats, attemptStats)
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "failed to retrieve all symbols", fields)
			return adaptors.DecodeResponse{}, stats, fmt.Errorf("failed to retrieve symbols: %w", err)
		}

		decodeInfo2, err2 := task.RQ.Decode(ctx, adaptors.DecodeRequest{
			ActionID: actionID,
			Symbols:  symbols,
			Layout:   layout,
		})
		if err2 != nil {
			return adaptors.DecodeResponse{}, stats, err2
		}
		return decodeInfo2, stats, nil
	}

	return adaptors.DecodeResponse{}, p2p.BatchRetrieveStats{}, fmt.Errorf("unsupported layout: expected 1 block, found %d", len(layout.Blocks))
}

func mergeRetrieveStats(base, addition p2p.BatchRetrieveStats) p2p.BatchRetrieveStats {
	if addition.TotalKeys != 0 {
		base.TotalKeys = addition.TotalKeys
	}
	if addition.Required != 0 {
		base.Required = addition.Required
	}
	if addition.FoundLocal != 0 {
		base.FoundLocal = addition.FoundLocal
	}
	base.FoundNetwork += addition.FoundNetwork
	if len(addition.Calls) > 0 {
		base.Calls = append(base.Calls, addition.Calls...)
	}
	return base
}
