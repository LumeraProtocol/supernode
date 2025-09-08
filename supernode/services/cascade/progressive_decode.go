package cascade

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
)

// retrieveAndDecodeProgressively progressively retrieves symbols and attempts decode at
// increasing thresholds to avoid over-fetching and reduce memory pressure.
//
// The progressive retrieval + decode loop originally lived in
// download.go::restoreFileFromLayout. It is moved here to isolate the control-flow from
// the main task logic, making Download/restoreFileFromLayout easier to follow and test.
//
// Prior implementation fetched a fixed minimum percentage and failed
// immediately on decode errors. In cases with symbol set skew or peer inconsistency, this
// could lead to repeated failures or fetching too many symbols upfront, increasing memory
// pressure. This helper escalates in steps (9%, 25%, 50%, 75%, 100%).
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

	// escalate retrieval targets
	percents := []int{requiredSymbolPercent, 25, 50, 75, 100}
	seen := map[int]struct{}{}
	ordered := make([]int, 0, len(percents))
	for _, p := range percents {
		if p < requiredSymbolPercent {
			continue
		}
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			ordered = append(ordered, p)
		}
	}

	// Build per-block symbol lists for balanced selection and compute total
	perBlock, totalSymbols := makePerBlock(layout)

	var lastErr error
	for _, p := range ordered {
		reqCount := (totalSymbols*p + 99) / 100
		fields["targetPercent"] = p
		fields["targetCount"] = reqCount
		logtrace.Info(ctx, "retrieving symbols for target percent", fields)

		// Build a balanced candidate key set ensuring some coverage per block.
		candidateKeys := selectBalancedKeys(perBlock, layout, reqCount, totalSymbols)

		symbols, err := task.P2PClient.BatchRetrieve(ctx, candidateKeys, reqCount, actionID)
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "failed to retrieve symbols", fields)
			return adaptors.DecodeResponse{}, fmt.Errorf("failed to retrieve symbols: %w", err)
		}
		fields["retrievedSymbols"] = len(symbols)
		logtrace.Info(ctx, "symbols retrieved", fields)

		// Attempt decode
		decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{
			ActionID: actionID,
			Symbols:  symbols,
			Layout:   layout,
		})
		if err == nil {
			return decodeInfo, nil
		}

		// Only escalate for probable insufficiency/integrity errors; otherwise, fail fast
		errStr := err.Error()
		if p >= 100 || !(strings.Contains(errStr, "decoding failed") ||
			strings.Contains(strings.ToLower(errStr), "hash mismatch") ||
			strings.Contains(strings.ToLower(errStr), "insufficient") ||
			strings.Contains(strings.ToLower(errStr), "symbol")) {
			fields[logtrace.FieldError] = errStr
			logtrace.Error(ctx, "failed to decode symbols", fields)
			return adaptors.DecodeResponse{}, fmt.Errorf("decode symbols using RaptorQ: %w", err)
		}

		logtrace.Info(ctx, "decode failed; escalating symbol target", logtrace.Fields{
			"last_error": errStr,
		})
		lastErr = err
	}

	if lastErr != nil {
		fields[logtrace.FieldError] = lastErr.Error()
		logtrace.Error(ctx, "failed to decode symbols after escalation", fields)
		return adaptors.DecodeResponse{}, fmt.Errorf("decode symbols using RaptorQ: %w", lastErr)
	}
	return adaptors.DecodeResponse{}, fmt.Errorf("decode symbols using RaptorQ: unknown failure")
}

// makePerBlock returns a copy of per-block symbol slices and the total symbol count.
func makePerBlock(layout codec.Layout) (map[int][]string, int) {
	perBlock := make(map[int][]string)
	total := 0
	for _, blk := range layout.Blocks {
		syms := make([]string, len(blk.Symbols))
		copy(syms, blk.Symbols)
		perBlock[blk.BlockID] = syms
		total += len(syms)
	}
	return perBlock, total
}

// selectBalancedKeys chooses up to reqCount keys ensuring at least 1 per block when possible,
// then distributing the remainder roughly proportional to block sizes, and finally filling
// any gap via round-robin. totalSymbols is the sum of all per-block lengths.
func selectBalancedKeys(perBlock map[int][]string, layout codec.Layout, reqCount int, totalSymbols int) []string {
	candidateKeys := make([]string, 0, reqCount)
	blocks := layout.Blocks
	// seed: at least one per block if possible
	for _, blk := range blocks {
		if len(candidateKeys) >= reqCount {
			break
		}
		syms := perBlock[blk.BlockID]
		if len(syms) > 0 {
			candidateKeys = append(candidateKeys, syms[0])
		}
	}
	remaining := reqCount - len(candidateKeys)
	if remaining <= 0 {
		return candidateKeys
	}
	// quotas per block
	quotas := make(map[int]int)
	sumQuotas := 0
	for _, blk := range blocks {
		blkTotal := len(perBlock[blk.BlockID])
		if blkTotal == 0 || totalSymbols == 0 {
			quotas[blk.BlockID] = 0
			continue
		}
		q := int(math.Ceil(float64(remaining) * float64(blkTotal) / float64(totalSymbols)))
		if q < 0 {
			q = 0
		}
		quotas[blk.BlockID] = q
		sumQuotas += q
	}
	// normalize overshoot
	for sumQuotas > remaining {
		for _, blk := range blocks {
			if sumQuotas <= remaining {
				break
			}
			if quotas[blk.BlockID] > 0 {
				quotas[blk.BlockID]--
				sumQuotas--
			}
		}
	}
	// select additional symbols according to quotas
	for _, blk := range blocks {
		want := quotas[blk.BlockID]
		if want <= 0 {
			continue
		}
		syms := perBlock[blk.BlockID]
		start := 1 // 0th may have been used in seed
		for i := start; i < len(syms) && want > 0 && len(candidateKeys) < reqCount; i++ {
			candidateKeys = append(candidateKeys, syms[i])
			want--
		}
	}
	// if still short (rounding/small blocks), round-robin fill
	if len(candidateKeys) < reqCount {
		for _, blk := range blocks {
			if len(candidateKeys) >= reqCount {
				break
			}
			syms := perBlock[blk.BlockID]
			for i := 0; i < len(syms) && len(candidateKeys) < reqCount; i++ {
				candidateKeys = append(candidateKeys, syms[i])
			}
		}
	}
	if len(candidateKeys) > reqCount {
		candidateKeys = candidateKeys[:reqCount]
	}
	return candidateKeys
}
