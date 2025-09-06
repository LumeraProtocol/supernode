package cascade

import (
    "context"
    "fmt"
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
    allSymbols []string,
    layout codec.Layout,
    actionID string,
    fields logtrace.Fields,
) (adaptors.DecodeResponse, error) {
    // Ensure base context fields are present for logs
    if fields == nil {
        fields = logtrace.Fields{}
    }
    fields[logtrace.FieldActionID] = actionID

    totalSymbols := len(allSymbols)
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

    var lastErr error
    for _, p := range ordered {
        reqCount := (totalSymbols*p + 99) / 100
        fields["targetPercent"] = p
        fields["targetCount"] = reqCount
        logtrace.Info(ctx, "retrieving symbols for target percent", fields)

        symbols, err := task.P2PClient.BatchRetrieve(ctx, allSymbols, reqCount, actionID)
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
        if p >= 100 || !(
            strings.Contains(errStr, "decoding failed") ||
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
