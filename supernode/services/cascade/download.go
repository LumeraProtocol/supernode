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
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
)

type DownloadRequest struct {
	ActionID string
}

type DownloadResponse struct {
	EventType     SupernodeEventType
	Message       string
	FilePath      string
	DownloadedDir string
}

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
		fields[logtrace.FieldError] = err
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}
	logtrace.Info(ctx, "Action retrieved", fields)
	task.streamDownloadEvent(SupernodeEventTypeActionRetrieved, "Action retrieved", "", "", send)

	if actionDetails.GetAction().State != actiontypes.ActionStateDone {
		err = errors.New("action is not in a valid state")
		fields[logtrace.FieldError] = "action state is not done yet"
		fields[logtrace.FieldActionState] = actionDetails.GetAction().State
		return task.wrapErr(ctx, "action not found", err, fields)
	}
	logtrace.Info(ctx, "Action state validated", fields)

	metadata, err := task.decodeCascadeMetadata(ctx, actionDetails.GetAction().Metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "Cascade metadata decoded", fields)
	task.streamDownloadEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", "", send)

	// Notify: network retrieval phase begins
	task.streamDownloadEvent(SupernodeEventTypeNetworkRetrieveStarted, "Network retrieval started", "", "", send)

	filePath, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to download artifacts", err, fields)
	}
	logtrace.Info(ctx, "File reconstructed and hash verified", fields)
	// Notify: decode completed, file ready on disk
	task.streamDownloadEvent(SupernodeEventTypeDecodeCompleted, "Decode completed", filePath, tmpDir, send)

	return nil
}

func (task *CascadeRegistrationTask) downloadArtifacts(ctx context.Context, actionID string, metadata actiontypes.CascadeMetadata, fields logtrace.Fields) (string, string, error) {
	logtrace.Info(ctx, "started downloading the artifacts", fields)

	var (
		layout         codec.Layout
		layoutFetchMS  int64
		layoutDecodeMS int64
		layoutAttempts int
	)

	for _, indexID := range metadata.RqIdsIds {
		indexFile, err := task.P2PClient.Retrieve(ctx, indexID)
		if err != nil || len(indexFile) == 0 {
			continue
		}

		// Parse index file to get layout IDs
		indexData, err := task.parseIndexFile(indexFile)
		if err != nil {
			logtrace.Info(ctx, "failed to parse index file", fields)
			continue
		}

		// Try to retrieve layout files using layout IDs from index file
		var netMS, decMS int64
		layout, netMS, decMS, layoutAttempts, err = task.retrieveLayoutFromIndex(ctx, indexData, fields)
		if err != nil {
			logtrace.Info(ctx, "failed to retrieve layout from index", fields)
			continue
		}
		layoutFetchMS = netMS
		layoutDecodeMS = decMS

		if len(layout.Blocks) > 0 {
			logtrace.Info(ctx, "layout file retrieved via index", fields)
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
	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID)
}

func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
) (string, string, error) {

	fields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
	}
	var allSymbols []string
	for _, block := range layout.Blocks {
		allSymbols = append(allSymbols, block.Symbols...)
	}
	sort.Strings(allSymbols)

	totalSymbols := len(allSymbols)
	fields["totalSymbols"] = totalSymbols
	logtrace.Info(ctx, "Retrieving all symbols for decode", fields)

	// Measure symbols batch retrieve duration
	retrieveStart := time.Now()
	symbols, err := task.P2PClient.BatchRetrieve(ctx, allSymbols, totalSymbols, actionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "batch retrieve failed", fields)
		return "", "", fmt.Errorf("batch retrieve symbols: %w", err)
	}
	retrieveMS := time.Since(retrieveStart).Milliseconds()

	// Measure decode duration
	decodeStart := time.Now()
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

	// Build new structured analytics payload
	found := len(symbols)
	if found < 0 {
		found = 0
	}
	failed := totalSymbols - found
	if failed < 0 {
		failed = 0
	}

	// Compute theoretical minimums from layout (sum over blocks of ceil(Size/65535))
	const symbolSize int64 = 65535
	var minRequired int
	for _, b := range layout.Blocks {
		if b.Size > 0 {
			minRequired += int((b.Size + symbolSize - 1) / symbolSize)
		}
	}
	minPct := 0.0
	if totalSymbols > 0 {
		minPct = (float64(minRequired) / float64(totalSymbols)) * 100.0
	}
	retrievedPct := 0.0
	if totalSymbols > 0 {
		retrievedPct = (float64(found) / float64(totalSymbols)) * 100.0
	}
	marginOverMin := retrievedPct - minPct
	if marginOverMin < 0 {
		marginOverMin = 0
	}

	// Try to enrich with DHT metrics snapshot (most recent) and aggregate per-node
	var dhtFoundLocal, dhtFoundNet int
	var dhtDurationMS int64
	type nodeAgg struct {
		IP              string  `json:"ip"`
		ID              string  `json:"id"`
		Address         string  `json:"address"`
		Calls           int     `json:"calls"`
		Successes       int     `json:"successes"`
		Failures        int     `json:"failures"`
		KeysTotal       int     `json:"keys_total"`
		DurationTotalMS int64   `json:"duration_total_ms"`
		KeysPct         float64 `json:"keys_pct"`
	}
	var nodesSummary []nodeAgg
	nodesTotal := 0
	if stats, err := task.P2PClient.Stats(ctx); err == nil {
		if dhtRaw, ok := stats["dht"].(map[string]any); ok {
			if metrics, ok := dhtRaw["dht_metrics"].(map[string]any); ok {
				if recent, ok := metrics["batch_retrieve_recent"].([]any); ok && len(recent) > 0 {
					if point, ok := recent[0].(map[string]any); ok {
						if v, ok := point["found_local"].(float64); ok {
							dhtFoundLocal = int(v)
						}
						if v, ok := point["found_network"].(float64); ok {
							dhtFoundNet = int(v)
						}
						switch dur := point["duration"].(type) {
						case float64:
							dhtDurationMS = int64(dur) / int64(time.Millisecond)
						case string:
							if parse, err := time.ParseDuration(dur); err == nil {
								dhtDurationMS = parse.Milliseconds()
							}
						}
						if nodes, ok := point["nodes"].([]any); ok {
							type bucket struct{ agg nodeAgg }
							byAddr := map[string]*bucket{}
							for _, n := range nodes {
								if m, ok := n.(map[string]any); ok {
									ip, _ := m["ip"].(string)
									id, _ := m["id"].(string)
									addr, _ := m["address"].(string)
									keys := 0
									if kv, ok := m["keys"].(float64); ok {
										keys = int(kv)
									}
									success := false
									if sv, ok := m["success"].(bool); ok {
										success = sv
									}
									durMS := int64(0)
									if dv, ok := m["duration_ms"].(float64); ok {
										durMS = int64(dv)
									}
									b := byAddr[addr]
									if b == nil {
										b = &bucket{agg: nodeAgg{IP: ip, ID: id, Address: addr}}
										byAddr[addr] = b
									}
									b.agg.Calls++
									if success {
										b.agg.Successes++
									} else {
										b.agg.Failures++
									}
									b.agg.KeysTotal += keys
									b.agg.DurationTotalMS += durMS
								}
							}
							nodesSummary = make([]nodeAgg, 0, len(byAddr))
							for _, b := range byAddr {
								if found > 0 {
									b.agg.KeysPct = (float64(b.agg.KeysTotal) / float64(found)) * 100.0
								}
								nodesSummary = append(nodesSummary, b.agg)
							}
							nodesTotal = len(byAddr)
						}
					}
				}
			}
		}
	}

	payload := map[string]any{
		"layout": map[string]any{
			"symbols_total":        totalSymbols,
			"min_required_symbols": minRequired,
			"min_required_pct":     minPct,
			"attempts":             fields["layout_attempts"],
			"fetch_ms":             fields["layout_fetch_ms"],
			"decode_ms":            fields["layout_decode_ms"],
		},
		"retrieve": map[string]any{
			"retrieved_total":     found,
			"missing_total":       failed,
			"retrieved_pct":       retrievedPct,
			"margin_over_min_pct": marginOverMin,
			"retrieve_ms":         retrieveMS,
			"decode_ms":           decodeMS,
		},
		"dht": map[string]any{
			"found_local":   dhtFoundLocal,
			"found_network": dhtFoundNet,
			"duration_ms":   dhtDurationMS,
			"nodes_total":   nodesTotal,
			"nodes":         nodesSummary,
		},
	}
	if b, err := json.Marshal(payload); err == nil {
		task.streamDownloadEvent(SupernodeEventTypeArtefactsDownloaded, string(b), "", "", func(resp *DownloadResponse) error { return nil })
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
		layoutFile, err := task.P2PClient.Retrieve(ctx, layoutID)
		totalFetchMS += time.Since(t0).Milliseconds()
		if err != nil || len(layoutFile) == 0 {
			continue
		}

		t1 := time.Now()
		layout, _, _, err := parseRQMetadataFile(layoutFile)
		totalDecodeMS += time.Since(t1).Milliseconds()
		if err != nil {
			continue
		}

		if len(layout.Blocks) > 0 {
			return layout, totalFetchMS, totalDecodeMS, attempts, nil
		}
	}

	return codec.Layout{}, totalFetchMS, totalDecodeMS, attempts, errors.New("no valid layout found in index")
}

func (task *CascadeRegistrationTask) CleanupDownload(ctx context.Context, actionID string) error {
	if actionID == "" {
		return errors.New("actionID is empty")
	}

	// For now, we use actionID as the directory path to maintain compatibility
	if err := os.RemoveAll(actionID); err != nil {
		return errors.Errorf("failed to delete download directory: %s, :%s", actionID, err.Error())
	}

	return nil
}
