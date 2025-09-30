package cascade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/crypto"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	cm "github.com/LumeraProtocol/supernode/v2/pkg/p2pmetrics"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
)

const targetRequiredPercent = 17

type DownloadRequest struct {
	ActionID string
	// Signature is required for private downloads. For public cascade
	// actions (metadata.Public == true), this is ignored.
	Signature string
}

type DownloadResponse struct {
	EventType     SupernodeEventType
	Message       string
	FilePath      string
	DownloadedDir string
}

// Download retrieves a cascade artefact by action ID.
//
// Authorization behavior:
//   - If the cascade metadata has Public = true, signature verification is skipped
//     and the file is downloadable by anyone.
//   - If Public = false, a valid download signature is required.
func (task *CascadeRegistrationTask) Download(
	ctx context.Context,
	req *DownloadRequest,
	send func(resp *DownloadResponse) error,
) (err error) {
	fields := logtrace.Fields{logtrace.FieldMethod: "Download", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "Cascade download request received", fields)

	// Ensure task status is finalized regardless of outcome
	defer func() {
		if err != nil {
			task.UpdateStatus(common.StatusTaskCanceled)
		} else {
			task.UpdateStatus(common.StatusTaskCompleted)
		}
		task.Cancel()
	}()

	actionDetails, err := task.LumeraClient.GetAction(ctx, req.ActionID)
	if err != nil {
		// Ensure error is logged as string for consistency
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}
	logtrace.Info(ctx, "Action retrieved", fields)
	task.streamDownloadEvent(SupernodeEventTypeActionRetrieved, "Action retrieved", "", "", send)

	if actionDetails.GetAction().State != actiontypes.ActionStateDone {
		// Return a clearer error message when action is not yet finalized
		err = errors.New("action is not in a valid state")
		fields[logtrace.FieldError] = "action state is not done yet"
		fields[logtrace.FieldActionState] = actionDetails.GetAction().State
		return task.wrapErr(ctx, "action not finalized yet", err, fields)
	}
	logtrace.Info(ctx, "Action state validated", fields)

	metadata, err := task.decodeCascadeMetadata(ctx, actionDetails.GetAction().Metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "Cascade metadata decoded", fields)
	task.streamDownloadEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", "", send)

	// Enforce download authorization based on metadata.Public
	// - If public: skip signature verification; allow anonymous downloads
	// - If private: require a valid signature
	if !metadata.Public {
		if req.Signature == "" {
			fields[logtrace.FieldError] = "missing signature for private download"
			// Provide a descriptive message without a fabricated root error
			return task.wrapErr(ctx, "private cascade requires a download signature", nil, fields)
		}
		if err := task.VerifyDownloadSignature(ctx, req.ActionID, req.Signature); err != nil {
			fields[logtrace.FieldError] = err.Error()
			return task.wrapErr(ctx, "failed to verify download signature", err, fields)
		}
		logtrace.Info(ctx, "Download signature verified for private cascade", fields)
	} else {
		logtrace.Info(ctx, "Public cascade: skipping download signature verification", fields)
	}

	// Notify: network retrieval phase begins
	task.streamDownloadEvent(SupernodeEventTypeNetworkRetrieveStarted, "Network retrieval started", "", "", send)

	logtrace.Info(ctx, "Starting network retrieval of artefacts", logtrace.Fields{logtrace.FieldActionID: actionDetails.GetAction().ActionID})
	filePath, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields, send)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		// Ensure temporary decode directory is cleaned if decode failed after being created
		if tmpDir != "" {
			if cerr := task.CleanupDownload(ctx, tmpDir); cerr != nil {
				logtrace.Warn(ctx, "cleanup of tmp dir after error failed", logtrace.Fields{"tmp_dir": tmpDir, logtrace.FieldError: cerr.Error()})
			}
		}
		return task.wrapErr(ctx, "failed to download artifacts", err, fields)
	}
	logtrace.Info(ctx, "File reconstructed and hash verified", fields)
	// Notify: decode completed, file ready on disk
	task.streamDownloadEvent(SupernodeEventTypeDecodeCompleted, "Decode completed", filePath, tmpDir, send)

	return nil
}

func (task *CascadeRegistrationTask) downloadArtifacts(ctx context.Context, actionID string, metadata actiontypes.CascadeMetadata, fields logtrace.Fields, send func(resp *DownloadResponse) error) (string, string, error) {
	logtrace.Info(ctx, "started downloading the artifacts", fields)

	var (
		layout         codec.Layout
		layoutFetchMS  int64
		layoutDecodeMS int64
		layoutAttempts int
	)

	for _, indexID := range metadata.RqIdsIds {
		iStart := time.Now()
		logtrace.Info(ctx, "RPC Retrieve index file", logtrace.Fields{"index_id": indexID})
		indexFile, err := task.P2PClient.Retrieve(ctx, indexID)
		if err != nil || len(indexFile) == 0 {
			logtrace.Warn(ctx, "Retrieve index file failed or empty", logtrace.Fields{"index_id": indexID, logtrace.FieldError: fmt.Sprintf("%v", err)})
			continue
		}
		logtrace.Info(ctx, "Retrieve index file completed", logtrace.Fields{"index_id": indexID, "bytes": len(indexFile), "ms": time.Since(iStart).Milliseconds()})

		// Parse index file to get layout IDs
		indexData, err := task.parseIndexFile(indexFile)
		if err != nil {
			logtrace.Warn(ctx, "failed to parse index file", logtrace.Fields{"index_id": indexID, logtrace.FieldError: err.Error()})
			continue
		}

		// Try to retrieve layout files using layout IDs from index file
		var netMS, decMS int64
		layout, netMS, decMS, layoutAttempts, err = task.retrieveLayoutFromIndex(ctx, indexData, fields)
		if err != nil {
			logtrace.Warn(ctx, "failed to retrieve layout from index", logtrace.Fields{"index_id": indexID, logtrace.FieldError: err.Error(), "attempts": layoutAttempts})
			continue
		}
		layoutFetchMS = netMS
		layoutDecodeMS = decMS

		if len(layout.Blocks) > 0 {
			logtrace.Info(ctx, "layout file retrieved via index", logtrace.Fields{"index_id": indexID, "attempts": layoutAttempts, "net_ms": layoutFetchMS, "decode_ms": layoutDecodeMS})
			break
		}
	}

	if len(layout.Blocks) == 0 {
		return "", "", errors.New("no symbols found in RQ metadata")
	}
	// Persist layout timing in fields for downstream metrics
	fields["layout_fetch_ms"] = layoutFetchMS
	fields["layout_decode_ms"] = layoutDecodeMS
	fields["layout_attempts"] = layoutAttempts
	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID, send)
}

// restoreFileFromLayout reconstructs the original file from the provided layout
// and a subset of retrieved symbols. The method deduplicates symbol identifiers
// before network retrieval to avoid redundant requests and ensure the requested
// count reflects unique symbols only.
func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
	send func(resp *DownloadResponse) error,
) (string, string, error) {

	fields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
	}
	// Deduplicate symbols across blocks to avoid redundant requests
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
	// Compute target requirement (reporting only; does not change behavior)
	targetRequiredCount := (totalSymbols*targetRequiredPercent + 99) / 100
	if targetRequiredCount < 1 && totalSymbols > 0 {
		targetRequiredCount = 1
	}
	logtrace.Info(ctx, "Retrieving target-required symbols for decode", logtrace.Fields{"total_symbols": totalSymbols, "target_required_percent": targetRequiredPercent, "target_required_count": targetRequiredCount})

	if !task.config.MetricsDisabled {
		cm.StartRetrieveCapture(actionID)
		defer cm.StopRetrieveCapture(actionID)
	}

	// Measure symbols batch retrieve duration
	retrieveStart := time.Now()
	// Tag context with metrics task ID (actionID)
	ctxRetrieve := cm.WithTaskID(ctx, actionID)
	// Retrieve only a fraction of symbols (targetRequiredCount) based on redundancy
	// The DHT will short-circuit once it finds the required number across the provided keys
	reqCount := targetRequiredCount
	if reqCount > totalSymbols {
		reqCount = totalSymbols
	}
	rStart := time.Now()
	logtrace.Info(ctx, "RPC BatchRetrieve symbols", logtrace.Fields{"action_id": actionID, "requested": reqCount, "total_candidates": totalSymbols})
	symbols, err := task.P2PClient.BatchRetrieve(ctxRetrieve, allSymbols, reqCount, actionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "batch retrieve failed", fields)
		return "", "", fmt.Errorf("batch retrieve symbols: %w", err)
	}
	retrieveMS := time.Since(retrieveStart).Milliseconds()
	logtrace.Info(ctx, "RPC BatchRetrieve completed", logtrace.Fields{"action_id": actionID, "received": len(symbols), "ms": time.Since(rStart).Milliseconds()})

	// Measure decode duration
	decodeStart := time.Now()
	dStart := time.Now()
	logtrace.Info(ctx, "RQ Decode start", logtrace.Fields{"action_id": actionID})
	decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{
		ActionID: actionID,
		Symbols:  symbols,
		Layout:   layout,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "decode failed", fields)
		return "", "", fmt.Errorf("decode symbols using RaptorQ: %w", err)
	}
	decodeMS := time.Since(decodeStart).Milliseconds()
	logtrace.Info(ctx, "RQ Decode completed", logtrace.Fields{"action_id": actionID, "ms": time.Since(dStart).Milliseconds(), "tmp_dir": decodeInfo.DecodeTmpDir, "file_path": decodeInfo.FilePath})

	// Set minimal retrieve summary and emit event strictly from internal collector
	if !task.config.MetricsDisabled {
		cm.SetRetrieveSummary(actionID, retrieveMS, decodeMS)
		payload := cm.BuildDownloadEventPayloadFromCollector(actionID)
		if retrieve, ok := payload["retrieve"].(map[string]any); ok {
			retrieve["target_required_percent"] = targetRequiredPercent
			retrieve["target_required_count"] = targetRequiredCount
			retrieve["total_symbols"] = totalSymbols
		}
		if b, err := json.MarshalIndent(payload, "", "  "); err == nil {
			task.streamDownloadEvent(SupernodeEventTypeArtefactsDownloaded, string(b), "", "", send)
		}
	} else {
		// Send minimal hardcoded event when metrics disabled
		task.streamDownloadEvent(SupernodeEventTypeArtefactsDownloaded, "Download completed (metrics disabled)", "", "", send)
	}

	fileHash, err := crypto.HashFileIncrementally(decodeInfo.FilePath, 0)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", fmt.Errorf("hash file: %w", err)
	}
	if fileHash == nil {
		fields[logtrace.FieldError] = "file hash is nil"
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", errors.New("file hash is nil")
	}

	err = task.verifyDataHash(ctx, fileHash, dataHash, fields)
	if err != nil {
		logtrace.Error(ctx, "failed to verify hash", fields)
		fields[logtrace.FieldError] = err.Error()
		return "", decodeInfo.DecodeTmpDir, err
	}
	// Log the state of the temporary decode directory
	if decodeInfo.DecodeTmpDir != "" {
		if set, derr := utils.ReadDirFilenames(decodeInfo.DecodeTmpDir); derr == nil {
			if left := len(set); left > 0 {
				logtrace.Info(ctx, "Decode tmp directory has files remaining", logtrace.Fields{"dir": decodeInfo.DecodeTmpDir, "left": left})
			} else {
				logtrace.Info(ctx, "Decode tmp directory is empty", logtrace.Fields{"dir": decodeInfo.DecodeTmpDir})
			}
		}
	}
	logtrace.Info(ctx, "File successfully restored and hash verified", fields)

	return decodeInfo.FilePath, decodeInfo.DecodeTmpDir, nil
}

func (task *CascadeRegistrationTask) streamDownloadEvent(eventType SupernodeEventType, msg string, filePath string, tmpDir string, send func(resp *DownloadResponse) error) {
	_ = send(&DownloadResponse{
		EventType:     eventType,
		Message:       msg,
		FilePath:      filePath,
		DownloadedDir: tmpDir,
	})
}

// parseIndexFile parses compressed index file to extract IndexFile structure
func (task *CascadeRegistrationTask) parseIndexFile(data []byte) (IndexFile, error) {
	decompressed, err := utils.ZstdDecompress(data)
	if err != nil {
		return IndexFile{}, errors.Errorf("decompress index file: %w", err)
	}

	// Parse decompressed data: base64IndexFile.signature.counter
	parts := bytes.Split(decompressed, []byte{SeparatorByte})
	if len(parts) < 2 {
		return IndexFile{}, errors.New("invalid index file format")
	}

	// Decode the base64 index file
	return decodeIndexFile(string(parts[0]))
}

// retrieveLayoutFromIndex retrieves layout file using layout IDs from index file
func (task *CascadeRegistrationTask) retrieveLayoutFromIndex(ctx context.Context, indexData IndexFile, fields logtrace.Fields) (codec.Layout, int64, int64, int, error) {
	// Try to retrieve layout files using layout IDs from index file
	var (
		totalFetchMS  int64
		totalDecodeMS int64
		attempts      int
	)
	for _, layoutID := range indexData.LayoutIDs {
		attempts++
		t0 := time.Now()
		logtrace.Info(ctx, "RPC Retrieve layout file", logtrace.Fields{"layout_id": layoutID, "attempt": attempts})
		layoutFile, err := task.P2PClient.Retrieve(ctx, layoutID)
		took := time.Since(t0).Milliseconds()
		totalFetchMS += took
		if err != nil || len(layoutFile) == 0 {
			logtrace.Warn(ctx, "Retrieve layout file failed or empty", logtrace.Fields{"layout_id": layoutID, "attempt": attempts, "ms": took, logtrace.FieldError: fmt.Sprintf("%v", err)})
			continue
		}

		t1 := time.Now()
		layout, _, _, err := parseRQMetadataFile(layoutFile)
		decMS := time.Since(t1).Milliseconds()
		totalDecodeMS += decMS
		if err != nil {
			logtrace.Warn(ctx, "Parse layout file failed", logtrace.Fields{"layout_id": layoutID, "attempt": attempts, "decode_ms": decMS, logtrace.FieldError: err.Error()})
			continue
		}

		if len(layout.Blocks) > 0 {
			logtrace.Info(ctx, "Layout file retrieved and parsed", logtrace.Fields{"layout_id": layoutID, "attempt": attempts, "net_ms": took, "decode_ms": decMS})
			return layout, totalFetchMS, totalDecodeMS, attempts, nil
		}
	}

	return codec.Layout{}, totalFetchMS, totalDecodeMS, attempts, errors.New("no valid layout found in index")
}

// CleanupDownload removes the temporary directory created during decode.
// The parameter is a directory path (not an action ID).
func (task *CascadeRegistrationTask) CleanupDownload(ctx context.Context, dirPath string) error {
	if dirPath == "" {
		return errors.New("directory path is empty")
	}

	// For now, we use tmp directory path as provided by decoder
	logtrace.Info(ctx, "Cleanup download directory", logtrace.Fields{"dir": dirPath})
	if err := os.RemoveAll(dirPath); err != nil {
		logtrace.Warn(ctx, "Cleanup download directory failed", logtrace.Fields{"dir": dirPath, logtrace.FieldError: err.Error()})
		return errors.Errorf("failed to delete download directory: %s, :%s", dirPath, err.Error())
	}
	logtrace.Info(ctx, "Cleanup download directory completed", logtrace.Fields{"dir": dirPath})

	return nil
}
