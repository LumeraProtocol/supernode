package cascade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/adaptors"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Step 0: Download reconstruction parameters.
//
// targetRequiredPercent is the initial minimum symbol coverage we require before the first decode attempt.
// Empirically, decode tends to succeed around ~17–18% for single-block cascades; we use a small buffer.
// Decode may require more than this, so the algorithm can progressively fetch more symbols.
const targetRequiredPercent = 20

// retrieveKeyFanoutFactor caps how many candidate keys we pass to BatchRetrieveStream relative to `need`.
// The DHT implementation does per-key preprocessing (e.g., base58 decode, routing/contact setup), so
// passing huge key lists when `need` is small can waste CPU and allocations.
// The fanout factor + minimum are chosen to keep a high probability of satisfying `need` even if some
// keys are unavailable.
const (
	retrieveKeyFanoutFactor  = 50
	retrieveKeyMinCandidates = 5000
)

type DownloadRequest struct {
	ActionID  string
	Signature string
}

type DownloadResponse struct {
	EventType     SupernodeEventType
	Message       string
	FilePath      string
	DownloadedDir string
}

func (task *CascadeRegistrationTask) Download(ctx context.Context, req *DownloadRequest, send func(resp *DownloadResponse) error) (err error) {
	if req != nil && req.ActionID != "" {
		ctx = logtrace.CtxWithCorrelationID(ctx, req.ActionID)
		ctx = logtrace.CtxWithOrigin(ctx, "download")
	}
	fields := logtrace.Fields{logtrace.FieldMethod: "Download", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "download: request", fields)

	// Step 1: Fetch action.
	actionDetails, err := task.LumeraClient.GetAction(ctx, req.ActionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}
	logtrace.Info(ctx, "download: action fetched", fields)
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeActionRetrieved, "Action retrieved", "", "", send); err != nil {
		return err
	}

	// Step 2: Validate action state.
	state := actionDetails.GetAction().State
	if state != actiontypes.ActionStateDone && state != actiontypes.ActionStateApproved {
		err = errors.New("action must be in DONE or APPROVED state to download")
		fields[logtrace.FieldError] = err.Error()
		fields[logtrace.FieldActionState] = state
		return task.wrapErr(ctx, "action not ready for download", err, fields)
	}
	logtrace.Info(ctx, "download: action state ok", fields)

	// Step 3: Decode cascade metadata.
	metadata, err := cascadekit.UnmarshalCascadeMetadata(actionDetails.GetAction().Metadata)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "download: metadata decoded", fields)
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", "", send); err != nil {
		return err
	}

	// Step 4: Verify download signature for private cascades.
	if !metadata.Public {
		if req.Signature == "" {
			fields[logtrace.FieldError] = "missing signature for private download"
			return task.wrapErr(ctx, "private cascade requires a download signature", nil, fields)
		}
		if err := task.VerifyDownloadSignature(ctx, req.ActionID, req.Signature); err != nil {
			fields[logtrace.FieldError] = err.Error()
			return task.wrapErr(ctx, "failed to verify download signature", err, fields)
		}
		logtrace.Info(ctx, "download: signature verified", fields)
	} else {
		logtrace.Info(ctx, "download: public cascade (no signature)", fields)
	}

	// Step 5: Emit retrieval-start event.
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeNetworkRetrieveStarted, "Network retrieval started", "", "", send); err != nil {
		return err
	}

	// Step 6: Resolve layout + retrieve symbols + decode + verify hash.
	logtrace.Info(ctx, "download: network retrieval start", logtrace.Fields{logtrace.FieldActionID: actionDetails.GetAction().ActionID})
	filePath, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields, send)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		if tmpDir != "" {
			// Step 7: Clean up temporary workspace on failure.
			if cerr := task.CleanupDownload(ctx, tmpDir); cerr != nil {
				logtrace.Warn(ctx, "cleanup of tmp dir after error failed", logtrace.Fields{"tmp_dir": tmpDir, logtrace.FieldError: cerr.Error()})
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return task.wrapErr(ctx, "failed to download artifacts", err, fields)
	}
	logtrace.Debug(ctx, "File reconstructed and hash verified", fields)

	// Step 8: Emit decode-completed event.
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeDecodeCompleted, "Decode completed", filePath, tmpDir, send); err != nil {
		if tmpDir != "" {
			if cerr := task.CleanupDownload(ctx, tmpDir); cerr != nil {
				logtrace.Warn(ctx, "cleanup of tmp dir after stream failure failed", logtrace.Fields{"tmp_dir": tmpDir, logtrace.FieldError: cerr.Error()})
			}
		}
		return err
	}

	return nil
}

func (task *CascadeRegistrationTask) CleanupDownload(ctx context.Context, tmpDir string) error {
	// Step 0: Best-effort cleanup of any temporary workspace created during download/decode.
	if tmpDir == "" {
		return nil
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	return nil
}

func (task *CascadeRegistrationTask) VerifyDownloadSignature(ctx context.Context, actionID, signature string) error {
	if signature == "" {
		return errors.New("signature required")
	}
	// Fetch the action to get the creator address for verification.
	act, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return fmt.Errorf("get action for signature verification: %w", err)
	}
	creator := act.GetAction().Creator
	appPubkey := act.GetAction().AppPubkey
	baseFields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
		logtrace.FieldCreator:  creator,
	}
	if hrp := strings.SplitN(creator, "1", 2); len(appPubkey) > 0 && len(hrp) == 2 && hrp[0] != "" {
		pubKey := secp256k1.PubKey{Key: appPubkey}
		addr, err := sdk.Bech32ifyAddressBytes(hrp[0], pubKey.Address())
		if err == nil {
			logtrace.Info(ctx, "download: app_pubkey derived address", logtrace.WithFields(baseFields, logtrace.Fields{
				"creator_prefix":  hrp[0],
				"app_pubkey_addr": addr,
			}))
		} else {
			logtrace.Debug(ctx, "download: app_pubkey address derivation failed", logtrace.WithFields(baseFields, logtrace.Fields{
				logtrace.FieldError: err.Error(),
			}))
		}
	}
	logtrace.Info(ctx, "download: signature verify start", logtrace.WithFields(baseFields, logtrace.Fields{
		"pubkey_len":  len(appPubkey),
		"sig_b64_len": len(signature),
	}))
	verify := task.buildSignatureVerifier(ctx, actionID, creator, appPubkey)
	if err := cascadekit.VerifyStringRawOrADR36(actionID, signature, creator, func(data, sig []byte) error {
		if vErr := verify(data, sig); vErr == nil {
			logtrace.Info(ctx, "download: signature verify ok", baseFields)
			return nil
		} else {
			logtrace.Debug(ctx, "download: signature verify attempt failed", logtrace.WithFields(baseFields, logtrace.Fields{
				logtrace.FieldError: vErr.Error(),
			}))
			return vErr
		}
	}); err != nil {
		logtrace.Warn(ctx, "download: signature verify failed", logtrace.WithFields(baseFields, logtrace.Fields{
			logtrace.FieldError: err.Error(),
		}))
		return err
	}
	return nil
}

func (task *CascadeRegistrationTask) streamDownloadEvent(ctx context.Context, eventType SupernodeEventType, msg, filePath, dir string, send func(resp *DownloadResponse) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return send(&DownloadResponse{EventType: eventType, Message: msg, FilePath: filePath, DownloadedDir: dir})
}

func (task *CascadeRegistrationTask) downloadArtifacts(ctx context.Context, actionID string, metadata actiontypes.CascadeMetadata, fields logtrace.Fields, send func(resp *DownloadResponse) error) (string, string, error) {
	var layout codec.Layout
	var layoutFetchMS, layoutDecodeMS int64
	var layoutAttempts int

	// Step 1: Retrieve index file(s) via index IDs.
	if len(metadata.RqIdsIds) > 0 {
		for _, indexID := range metadata.RqIdsIds {
			iStart := time.Now()
			logtrace.Debug(ctx, "RPC Retrieve index file", logtrace.Fields{"index_id": indexID})
			indexFile, err := task.P2PClient.Retrieve(ctx, indexID)
			if err != nil || len(indexFile) == 0 {
				logtrace.Warn(ctx, "Retrieve index file failed or empty", logtrace.Fields{"index_id": indexID, logtrace.FieldError: fmt.Sprintf("%v", err)})
				continue
			}
			logtrace.Debug(ctx, "Retrieve index file completed", logtrace.Fields{"index_id": indexID, "bytes": len(indexFile), "ms": time.Since(iStart).Milliseconds()})
			indexData, err := cascadekit.ParseCompressedIndexFile(indexFile)
			if err != nil {
				logtrace.Warn(ctx, "failed to parse index file", logtrace.Fields{"index_id": indexID, logtrace.FieldError: err.Error()})
				continue
			}
			var netMS, decMS int64
			var attempts int

			// Step 2: Resolve and decode layout referenced by the index.
			layout, netMS, decMS, attempts, err = task.retrieveLayoutFromIndex(ctx, indexData, fields)
			if err != nil {
				logtrace.Warn(ctx, "failed to retrieve layout from index", logtrace.Fields{"index_id": indexID, logtrace.FieldError: err.Error(), "attempts": attempts})
				continue
			}
			layoutFetchMS, layoutDecodeMS, layoutAttempts = netMS, decMS, attempts
			if len(layout.Blocks) > 0 {
				logtrace.Debug(ctx, "layout file retrieved via index", logtrace.Fields{"index_id": indexID, "attempts": attempts, "net_ms": layoutFetchMS, "decode_ms": layoutDecodeMS})
				break
			}
		}
	}
	if len(layout.Blocks) == 0 {
		return "", "", errors.New("no symbols found in RQ metadata")
	}
	fields["layout_fetch_ms"], fields["layout_decode_ms"], fields["layout_attempts"] = layoutFetchMS, layoutDecodeMS, layoutAttempts

	// Step 3: Reconstruct file from layout + symbols; verify hash.
	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID, send)
}

func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
	send func(resp *DownloadResponse) error,
) (string, string, error) {
	fields := logtrace.Fields{logtrace.FieldActionID: actionID}

	// Step 1: Build symbol candidate list from the layout.
	// Prefer layout order for single-block Cascade (reduces "missing open" churn vs lexicographic sorts).
	var allSymbols []string
	writeBlockID := -1
	if len(layout.Blocks) == 1 {
		writeBlockID = layout.Blocks[0].BlockID
		allSymbols = make([]string, 0, len(layout.Blocks[0].Symbols))
		seen := make(map[string]struct{}, len(layout.Blocks[0].Symbols))
		for _, s := range layout.Blocks[0].Symbols {
			if s == "" {
				continue
			}
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			allSymbols = append(allSymbols, s)
		}
	} else {
		symSet := make(map[string]struct{}, 1024)
		for _, block := range layout.Blocks {
			for _, s := range block.Symbols {
				if s == "" {
					continue
				}
				symSet[s] = struct{}{}
			}
		}
		allSymbols = make([]string, 0, len(symSet))
		for s := range symSet {
			allSymbols = append(allSymbols, s)
		}
		sort.Strings(allSymbols)
	}
	totalSymbols := len(allSymbols)
	fields["totalSymbols"] = totalSymbols

	// Step 2: Compute initial required symbol count (ceil(targetRequiredPercent%)).
	targetRequiredCount := (totalSymbols*targetRequiredPercent + 99) / 100
	if targetRequiredCount < 1 && totalSymbols > 0 {
		targetRequiredCount = 1
	}
	if targetRequiredCount > totalSymbols {
		targetRequiredCount = totalSymbols
	}
	logtrace.Info(ctx, "download: plan symbols",
		logtrace.Fields{"total_symbols": totalSymbols, "target_required_percent": targetRequiredPercent, "target_required_count": targetRequiredCount})

	if totalSymbols == 0 {
		return "", "", errors.New("no symbols present in layout")
	}

	// Step 3: Prepare RQ workspace once; stream symbols into it across attempts.
	// Prepare RQ workspace once; stream symbols directly into it, and retry decode by fetching more.
	logtrace.Info(ctx, "download: prepare RQ workspace", logtrace.Fields{"action_id": actionID})
	_, writeSymbol, cleanup, ws, perr := task.RQ.PrepareDecode(ctx, actionID, layout)
	if perr != nil {
		fields[logtrace.FieldError] = perr.Error()
		logtrace.Error(ctx, "rq prepare-decode failed", fields)
		return "", "", fmt.Errorf("prepare decode: %w", perr)
	}
	success := false
	defer func() {
		if !success && cleanup != nil {
			_ = cleanup()
		}
	}()

	var writtenSet sync.Map // base58 symbol id -> struct{}
	var written int32
	onSymbol := func(symbolID string, data []byte) error {
		// Write each retrieved symbol into the prepared workspace.
		// Count unique symbol IDs only, so progress reflects real coverage (not duplicates).
		// In the single-block case, write directly to the known block to avoid per-symbol block lookup.
		if _, err := writeSymbol(writeBlockID, symbolID, data); err != nil {
			return err
		}
		if _, loaded := writtenSet.LoadOrStore(symbolID, struct{}{}); !loaded {
			atomic.AddInt32(&written, 1)
		}
		return nil
	}

	retrieveStart := time.Now()
	reqCount := targetRequiredCount
	step := (totalSymbols*5 + 99) / 100 // +5% of total symbols (rounded up)
	if step < 1 {
		step = 1
	}
	const maxDecodeAttempts = 4

	var decodeInfo adaptors.DecodeResult
	var lastDecodeErr error
	for attempt := 1; attempt <= maxDecodeAttempts; attempt++ {
		// Step 4.1: Determine how many unique symbols we have vs the current target.
		have := int(atomic.LoadInt32(&written))
		need := reqCount - have
		if need > 0 {
			// Step 4.2: Fetch exactly the delta (`need`) from not-yet-written candidate keys.
			// Select a candidate slice to spread load across the keyspace; fall back to all keys if needed.
			// Start with a smaller candidate set; expand if we don't have enough remaining keys.
			candidates := allSymbols
			if want := reqCount * 2; want < len(allSymbols) {
				candidateCount := want
				maxStart := len(allSymbols) - candidateCount
				start := ((attempt - 1) * candidateCount) % (maxStart + 1)
				candidates = allSymbols[start : start+candidateCount]
			}

			// Cap how many keys we pass to the DHT when `need` is small, and stop scanning once reached.
			keyCap := need * retrieveKeyFanoutFactor
			if keyCap < retrieveKeyMinCandidates {
				keyCap = retrieveKeyMinCandidates
			}
			if keyCap < need {
				keyCap = need
			}
			remainingKeys := make([]string, 0, min(keyCap, len(candidates)))
			for _, k := range candidates {
				if k == "" {
					continue
				}
				if _, ok := writtenSet.Load(k); ok {
					continue
				}
				remainingKeys = append(remainingKeys, k)
				if len(remainingKeys) >= keyCap {
					break
				}
			}

			if len(remainingKeys) < need && len(candidates) < len(allSymbols) {
				// Fall back to all symbols to avoid getting stuck on a narrow prefix.
				remainingKeys = remainingKeys[:0]
				for _, k := range allSymbols {
					if k == "" {
						continue
					}
					if _, ok := writtenSet.Load(k); ok {
						continue
					}
					remainingKeys = append(remainingKeys, k)
					if len(remainingKeys) >= keyCap {
						break
					}
				}
			}

			logtrace.Info(ctx, "download: batch retrieve start", logtrace.Fields{
				"action_id":  actionID,
				"attempt":    attempt,
				"requested":  need,
				"have":       have,
				"target":     reqCount,
				"candidates": len(candidates),
				"keys":       len(remainingKeys),
			})
			rStart := time.Now()
			if _, rerr := task.P2PClient.BatchRetrieveStream(ctx, remainingKeys, int32(need), actionID, onSymbol); rerr != nil {
				fields[logtrace.FieldError] = rerr.Error()
				logtrace.Error(ctx, "batch retrieve stream failed", fields)
				return "", ws.SymbolsDir, fmt.Errorf("batch retrieve stream: %w", rerr)
			}
			logtrace.Info(ctx, "download: batch retrieve ok", logtrace.Fields{
				"action_id": actionID,
				"attempt":   attempt,
				"ms":        time.Since(rStart).Milliseconds(),
				"have":      atomic.LoadInt32(&written),
			})
		}

		// Don't spend CPU attempting decode until we have at least the requested count for this attempt.
		// Step 4.3: Skip decode until `have >= reqCount`.
		have = int(atomic.LoadInt32(&written))
		if have < reqCount {
			lastDecodeErr = fmt.Errorf("insufficient symbols to attempt decode: have %d want %d", have, reqCount)
			logtrace.Warn(ctx, "download: skip decode; insufficient symbols", logtrace.Fields{
				"action_id": actionID,
				"attempt":   attempt,
				"received":  have,
				"target":    reqCount,
			})
			continue
		}

		decodeStart := time.Now()
		// Step 4.4: Attempt decode from the prepared workspace.
		logtrace.Info(ctx, "download: decode start", logtrace.Fields{
			"action_id": actionID,
			"attempt":   attempt,
			"received":  atomic.LoadInt32(&written),
			"target":    reqCount,
		})
		decodeInfo, lastDecodeErr = task.RQ.DecodeFromPrepared(ctx, ws, layout)
		if lastDecodeErr == nil {
			retrieveMS := time.Since(retrieveStart).Milliseconds()
			decodeMS := time.Since(decodeStart).Milliseconds()
			logtrace.Info(ctx, "download: decode ok", logtrace.Fields{
				"action_id": actionID,
				"attempt":   attempt,
				"ms":        decodeMS,
				"tmp_dir":   decodeInfo.DecodeTmpDir,
				"file_path": decodeInfo.FilePath,
			})
			logtrace.Debug(ctx, "download: timing", logtrace.Fields{"action_id": actionID, "retrieve_ms": retrieveMS, "decode_ms": decodeMS})
			break
		}

		fields[logtrace.FieldError] = lastDecodeErr.Error()
		logtrace.Warn(ctx, "decode failed; will fetch more symbols and retry", logtrace.Fields{
			"action_id": actionID,
			"attempt":   attempt,
			"received":  atomic.LoadInt32(&written),
			"target":    reqCount,
			"err":       lastDecodeErr.Error(),
		})

		if reqCount >= totalSymbols {
			return "", ws.SymbolsDir, fmt.Errorf("decode symbols using RaptorQ: %w", lastDecodeErr)
		}
		reqCount += step
		if reqCount > totalSymbols {
			reqCount = totalSymbols
		}
	}

	if decodeInfo.FilePath == "" {
		if lastDecodeErr != nil {
			return "", ws.SymbolsDir, fmt.Errorf("decode symbols using RaptorQ: %w", lastDecodeErr)
		}
		return "", ws.SymbolsDir, errors.New("decode failed after retries")
	}

	// Step 5: Verify reconstructed file hash matches action metadata.
	// 3) Verify hash
	fileHash, herr := utils.Blake3HashFile(decodeInfo.FilePath)
	if herr != nil {
		fields[logtrace.FieldError] = herr.Error()
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", decodeInfo.DecodeTmpDir, fmt.Errorf("hash file: %w", herr)
	}
	if fileHash == nil {
		fields[logtrace.FieldError] = "file hash is nil"
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", decodeInfo.DecodeTmpDir, errors.New("file hash is nil")
	}
	if verr := cascadekit.VerifyB64DataHash(fileHash, dataHash); verr != nil {
		fields[logtrace.FieldError] = verr.Error()
		logtrace.Error(ctx, "failed to verify hash", fields)
		return "", decodeInfo.DecodeTmpDir, verr
	}

	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "download: file verified", fields)

	// Step 6: Emit final download event and return.
	// Event
	info := map[string]interface{}{"action_id": actionID, "found_symbols": atomic.LoadInt32(&written), "target_percent": targetRequiredPercent}
	if b, err := json.Marshal(info); err == nil {
		if err := task.streamDownloadEvent(ctx, SupernodeEventTypeArtefactsDownloaded, string(b), decodeInfo.FilePath, decodeInfo.DecodeTmpDir, send); err != nil {
			return "", decodeInfo.DecodeTmpDir, err
		}
	}

	success = true
	return decodeInfo.FilePath, decodeInfo.DecodeTmpDir, nil
}
