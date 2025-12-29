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
)

const targetRequiredPercent = 17

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

	actionDetails, err := task.LumeraClient.GetAction(ctx, req.ActionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}
	logtrace.Info(ctx, "download: action fetched", fields)
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeActionRetrieved, "Action retrieved", "", "", send); err != nil {
		return err
	}

	if actionDetails.GetAction().State != actiontypes.ActionStateDone {
		err = errors.New("action is not in a valid state")
		fields[logtrace.FieldError] = "action state is not done yet"
		fields[logtrace.FieldActionState] = actionDetails.GetAction().State
		return task.wrapErr(ctx, "action not finalized yet", err, fields)
	}
	logtrace.Info(ctx, "download: action state ok", fields)

	metadata, err := cascadekit.UnmarshalCascadeMetadata(actionDetails.GetAction().Metadata)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "download: metadata decoded", fields)
	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", "", send); err != nil {
		return err
	}

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

	if err := task.streamDownloadEvent(ctx, SupernodeEventTypeNetworkRetrieveStarted, "Network retrieval started", "", "", send); err != nil {
		return err
	}

	logtrace.Info(ctx, "download: network retrieval start", logtrace.Fields{logtrace.FieldActionID: actionDetails.GetAction().ActionID})
	filePath, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields, send)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		if tmpDir != "" {
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
	// Fetch the action to get the creator address for verification
	act, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return fmt.Errorf("get action for signature verification: %w", err)
	}
	creator := act.GetAction().Creator
	if err := cascadekit.VerifyStringRawOrADR36(actionID, signature, creator, func(data, sig []byte) error {
		return task.LumeraClient.Verify(ctx, creator, data, sig)
	}); err != nil {
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

	// Retrieve via index IDs
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
	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID, send)
}

func (task *CascadeRegistrationTask) restoreFileFromLayoutDeprecated(ctx context.Context, layout codec.Layout, dataHash string, actionID string, send func(resp *DownloadResponse) error) (string, string, error) {
	fields := logtrace.Fields{logtrace.FieldActionID: actionID}
	symSet := make(map[string]struct{})
	for _, block := range layout.Blocks {
		for _, s := range block.Symbols {
			symSet[s] = struct{}{}
		}
	}
	allSymbols := make([]string, 0, len(symSet))
	for s := range symSet {
		allSymbols = append(allSymbols, s)
	}
	sort.Strings(allSymbols)
	totalSymbols := len(allSymbols)
	fields["totalSymbols"] = totalSymbols
	targetRequiredCount := (totalSymbols*targetRequiredPercent + 99) / 100
	if targetRequiredCount < 1 && totalSymbols > 0 {
		targetRequiredCount = 1
	}
	logtrace.Info(ctx, "download: plan symbols", logtrace.Fields{"total_symbols": totalSymbols, "target_required_percent": targetRequiredPercent, "target_required_count": targetRequiredCount})
	retrieveStart := time.Now()
	reqCount := targetRequiredCount
	if reqCount > totalSymbols {
		reqCount = totalSymbols
	}
	rStart := time.Now()
	logtrace.Info(ctx, "download: batch retrieve start", logtrace.Fields{"action_id": actionID, "requested": reqCount, "total_candidates": totalSymbols})
	symbols, err := task.P2PClient.BatchRetrieve(ctx, allSymbols, reqCount, actionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "batch retrieve failed", fields)
		return "", "", fmt.Errorf("batch retrieve symbols: %w", err)
	}
	retrieveMS := time.Since(retrieveStart).Milliseconds()
	logtrace.Info(ctx, "download: batch retrieve ok", logtrace.Fields{"action_id": actionID, "received": len(symbols), "ms": time.Since(rStart).Milliseconds()})
	decodeStart := time.Now()
	dStart := time.Now()
	logtrace.Info(ctx, "download: decode start", logtrace.Fields{"action_id": actionID})
	decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{ActionID: actionID, Symbols: symbols, Layout: layout})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "decode failed", fields)
		return "", "", fmt.Errorf("decode symbols using RaptorQ: %w", err)
	}
	decodeMS := time.Since(decodeStart).Milliseconds()
	logtrace.Info(ctx, "download: decode ok", logtrace.Fields{"action_id": actionID, "ms": time.Since(dStart).Milliseconds(), "tmp_dir": decodeInfo.DecodeTmpDir, "file_path": decodeInfo.FilePath})
	// Emit timing metrics for network retrieval and decode phases
	logtrace.Debug(ctx, "download: timing", logtrace.Fields{"action_id": actionID, "retrieve_ms": retrieveMS, "decode_ms": decodeMS})

	// Verify reconstructed file hash matches action metadata
	fileHash, herr := utils.Blake3HashFile(decodeInfo.FilePath)
	if herr != nil {
		fields[logtrace.FieldError] = herr.Error()
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", fmt.Errorf("hash file: %w", herr)
	}
	if fileHash == nil {
		fields[logtrace.FieldError] = "file hash is nil"
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", errors.New("file hash is nil")
	}
	if verr := cascadekit.VerifyB64DataHash(fileHash, dataHash); verr != nil {
		fields[logtrace.FieldError] = verr.Error()
		logtrace.Error(ctx, "failed to verify hash", fields)
		return "", decodeInfo.DecodeTmpDir, verr
	}
	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "download: file verified", fields)
	// Emit minimal JSON payload (metrics system removed)
	info := map[string]interface{}{"action_id": actionID, "found_symbols": len(symbols), "target_percent": targetRequiredPercent}
	if b, err := json.Marshal(info); err == nil {
		if err := task.streamDownloadEvent(ctx, SupernodeEventTypeArtefactsDownloaded, string(b), decodeInfo.FilePath, decodeInfo.DecodeTmpDir, send); err != nil {
			return "", decodeInfo.DecodeTmpDir, err
		}
	}
	return decodeInfo.FilePath, decodeInfo.DecodeTmpDir, nil
}

func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
	send func(resp *DownloadResponse) error,
) (string, string, error) {

	fields := logtrace.Fields{logtrace.FieldActionID: actionID}

	// Unique symbols
	symSet := make(map[string]struct{}, 1024)
	for _, block := range layout.Blocks {
		for _, s := range block.Symbols {
			symSet[s] = struct{}{}
		}
	}
	allSymbols := make([]string, 0, len(symSet))
	for s := range symSet {
		allSymbols = append(allSymbols, s)
	}
	sort.Strings(allSymbols)
	totalSymbols := len(allSymbols)
	fields["totalSymbols"] = totalSymbols

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

	// Prepare RQ workspace once; stream symbols directly into it
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

	// Track exactly which symbol IDs we wrote (base58 IDs)
	var written int32
	var writtenSet sync.Map // b58 symbol id -> struct{}
	onSymbol := func(symbolID string, data []byte) error {
		if _, err := writeSymbol(-1, symbolID, data); err != nil {
			return err
		}
		writtenSet.Store(symbolID, struct{}{})
		atomic.AddInt32(&written, 1)
		return nil
	}

	// 1) Local batched streaming
	retrieveStart := time.Now()
	logtrace.Info(ctx, "download: local scan start", logtrace.Fields{"action_id": actionID, "requested": targetRequiredCount, "total_candidates": totalSymbols})
	localFound, lerr := task.P2PClient.BatchRetrieveStream(ctx, allSymbols, int32(targetRequiredCount), actionID, onSymbol, true)
	if lerr != nil && !strings.Contains(strings.ToLower(lerr.Error()), "local-only") {
		fields[logtrace.FieldError] = lerr.Error()
		logtrace.Error(ctx, "local batch retrieve stream failed", fields)
		return "", ws.SymbolsDir, fmt.Errorf("local batch retrieve stream: %w", lerr)
	}

	// If needed, compute the remaining keys that were NOT written in pass 1
	if int(localFound) < targetRequiredCount {
		remaining := int32(targetRequiredCount) - localFound

		// Build a compact slice of only the symbols not written by the local pass
		remainingKeys := make([]string, 0, len(allSymbols))
		for _, k := range allSymbols {
			if _, ok := writtenSet.Load(k); !ok {
				remainingKeys = append(remainingKeys, k)
			}
		}

		logtrace.Info(ctx, "download: network retrieve start", logtrace.Fields{
			"action_id": actionID, "remaining": remaining, "candidate_keys": len(remainingKeys),
		})

		if len(remainingKeys) == 0 {
			logtrace.Warn(ctx, "no remaining keys after local pass but remaining > 0; proceeding with allSymbols as fallback",
				logtrace.Fields{"action_id": actionID, "remaining": remaining})
			remainingKeys = allSymbols
		}

		// Network phase on only the remaining keys; avoids a second local scan & duplicate writes
		if _, nerr := task.P2PClient.BatchRetrieveStream(ctx, remainingKeys, remaining, actionID, onSymbol /* network allowed */); nerr != nil {
			fields[logtrace.FieldError] = nerr.Error()
			logtrace.Error(ctx, "network batch retrieve stream failed", fields)
			return "", ws.SymbolsDir, fmt.Errorf("network batch retrieve stream: %w", nerr)
		}
	}

	retrieveMS := time.Since(retrieveStart).Milliseconds()
	logtrace.Info(ctx, "download: batch retrieve (stream) ok", logtrace.Fields{
		"action_id": actionID, "received": atomic.LoadInt32(&written), "retrieve_ms": retrieveMS,
	})

	// 2) Decode from prepared workspace
	decodeStart := time.Now()
	logtrace.Info(ctx, "download: decode start", logtrace.Fields{"action_id": actionID})
	decodeInfo, derr := task.RQ.DecodeFromPrepared(ctx, ws, layout)
	if derr != nil {
		fields[logtrace.FieldError] = derr.Error()
		logtrace.Error(ctx, "decode failed", fields)
		return "", ws.SymbolsDir, fmt.Errorf("decode RaptorQ: %w", derr)
	}
	decodeMS := time.Since(decodeStart).Milliseconds()
	logtrace.Info(ctx, "download: decode ok", logtrace.Fields{
		"action_id": actionID, "ms": decodeMS, "tmp_dir": decodeInfo.DecodeTmpDir, "file_path": decodeInfo.FilePath,
	})
	logtrace.Debug(ctx, "download: timing", logtrace.Fields{"action_id": actionID, "retrieve_ms": retrieveMS, "decode_ms": decodeMS})

	// 3) Verify hash
	fileHash, herr := utils.Blake3HashFile(decodeInfo.FilePath)
	if herr != nil {
		fields[logtrace.FieldError] = herr.Error()
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", ws.SymbolsDir, fmt.Errorf("hash file: %w", herr)
	}
	if fileHash == nil {
		fields[logtrace.FieldError] = "file hash is nil"
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", ws.SymbolsDir, errors.New("file hash is nil")
	}
	if verr := cascadekit.VerifyB64DataHash(fileHash, dataHash); verr != nil {
		fields[logtrace.FieldError] = verr.Error()
		logtrace.Error(ctx, "failed to verify hash", fields)
		return "", decodeInfo.DecodeTmpDir, verr
	}

	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "download: file verified", fields)

	// Event
	info := map[string]interface{}{"action_id": actionID, "found_symbols": int(atomic.LoadInt32(&written)), "target_percent": targetRequiredPercent}
	if b, err := json.Marshal(info); err == nil {
		if err := task.streamDownloadEvent(ctx, SupernodeEventTypeArtefactsDownloaded, string(b), decodeInfo.FilePath, decodeInfo.DecodeTmpDir, send); err != nil {
			return "", decodeInfo.DecodeTmpDir, err
		}
	}

	success = true
	return decodeInfo.FilePath, decodeInfo.DecodeTmpDir, nil
}
