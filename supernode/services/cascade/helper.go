package cascade

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/golang/protobuf/proto"
	json "github.com/json-iterator/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RaptorQ symbol size for minimum K calculation (bytes).
const rqSymbolByteSize int64 = 65535

// nodeAgg aggregates per-node metrics for event payloads.
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

// computeLayoutStats returns total symbols T, minimum required symbols K and K% based solely on the layout.
func computeLayoutStats(layout codec.Layout) (symbolsTotal int, minRequired int, minPct float64) {
	for _, b := range layout.Blocks {
		symbolsTotal += len(b.Symbols)
		if b.Size > 0 {
			minRequired += int((b.Size + rqSymbolByteSize - 1) / rqSymbolByteSize)
		}
	}
	if symbolsTotal > 0 {
		minPct = (float64(minRequired) / float64(symbolsTotal)) * 100.0
	}
	return
}

// aggregateStoreCalls aggregates adaptor StoreCallMetric entries per node and computes key share percentage using denom.
func aggregateStoreCalls(calls []adaptors.StoreCallMetric, denom int) ([]nodeAgg, int) {
	type bucket struct{ agg nodeAgg }
	byAddr := map[string]*bucket{}
	for _, c := range calls {
		b := byAddr[c.Address]
		if b == nil {
			b = &bucket{agg: nodeAgg{IP: c.IP, ID: c.ID, Address: c.Address}}
			byAddr[c.Address] = b
		}
		b.agg.Calls++
		if c.Success {
			b.agg.Successes++
		} else {
			b.agg.Failures++
		}
		b.agg.KeysTotal += c.Keys
		b.agg.DurationTotalMS += c.DurationMS
	}
	out := make([]nodeAgg, 0, len(byAddr))
	for _, b := range byAddr {
		if denom > 0 {
			b.agg.KeysPct = (float64(b.agg.KeysTotal) / float64(denom)) * 100.0
		}
		out = append(out, b.agg)
	}
	return out, len(byAddr)
}

// aggregateDHTRecent folds the most recent DHT batch retrieve point and aggregates per-node metrics.
func aggregateDHTRecent(stats map[string]any, denom int) (foundLocal int, foundNetwork int, durationMS int64, nodes []nodeAgg, nodesTotal int) {
	nodes = nil
	dhtRaw, ok := stats["dht"].(map[string]any)
	if !ok {
		return
	}
	metrics, ok := dhtRaw["dht_metrics"].(map[string]any)
	if !ok {
		return
	}
	recent, ok := metrics["batch_retrieve_recent"].([]any)
	if !ok || len(recent) == 0 {
		return
	}
	point, ok := recent[0].(map[string]any)
	if !ok {
		return
	}
	if v, ok := point["found_local"].(float64); ok {
		foundLocal = int(v)
	}
	if v, ok := point["found_network"].(float64); ok {
		foundNetwork = int(v)
	}
	switch dur := point["duration"].(type) {
	case float64:
		durationMS = int64(dur) / int64(time.Millisecond)
	case string:
		if parse, err := time.ParseDuration(dur); err == nil {
			durationMS = parse.Milliseconds()
		}
	}
	type bucket struct{ agg nodeAgg }
	byAddr := map[string]*bucket{}
	if arr, ok := point["nodes"].([]any); ok {
		for _, n := range arr {
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
	}
	nodes = make([]nodeAgg, 0, len(byAddr))
	for _, b := range byAddr {
		if denom > 0 {
			b.agg.KeysPct = (float64(b.agg.KeysTotal) / float64(denom)) * 100.0
		}
		nodes = append(nodes, b.agg)
	}
	nodesTotal = len(byAddr)
	return
}

func (task *CascadeRegistrationTask) fetchAction(ctx context.Context, actionID string, f logtrace.Fields) (*actiontypes.Action, error) {
	res, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to get action", err, f)
	}

	if res.GetAction().ActionID == "" {
		return nil, task.wrapErr(ctx, "action not found", errors.New(""), f)
	}
	logtrace.Info(ctx, "action has been retrieved", f)

	return res.GetAction(), nil
}

func (task *CascadeRegistrationTask) ensureIsTopSupernode(ctx context.Context, blockHeight uint64, f logtrace.Fields) error {
	top, err := task.LumeraClient.GetTopSupernodes(ctx, blockHeight)
	if err != nil {
		return task.wrapErr(ctx, "failed to get top SNs", err, f)
	}
	logtrace.Info(ctx, "Fetched Top Supernodes", f)

	if !supernode.Exists(top.Supernodes, task.config.SupernodeAccountAddress) {
		// Build information about supernodes for better error context
		addresses := make([]string, len(top.Supernodes))
		for i, sn := range top.Supernodes {
			addresses[i] = sn.SupernodeAccount
		}
		logtrace.Info(ctx, "Supernode not in top list", logtrace.Fields{
			"currentAddress": task.config.SupernodeAccountAddress,
			"topSupernodes":  addresses,
		})
		return task.wrapErr(ctx, "current supernode does not exist in the top SNs list",
			errors.Errorf("current address: %s, top supernodes: %v", task.config.SupernodeAccountAddress, addresses), f)
	}

	return nil
}

func (task *CascadeRegistrationTask) decodeCascadeMetadata(ctx context.Context, raw []byte, f logtrace.Fields) (actiontypes.CascadeMetadata, error) {
	var meta actiontypes.CascadeMetadata
	if err := proto.Unmarshal(raw, &meta); err != nil {
		return meta, task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, f)
	}
	return meta, nil
}

func (task *CascadeRegistrationTask) verifyDataHash(ctx context.Context, dh []byte, expected string, f logtrace.Fields) error {
	b64 := utils.B64Encode(dh)
	if string(b64) != expected {
		return task.wrapErr(ctx, "data hash doesn't match", errors.New(""), f)
	}
	logtrace.Info(ctx, "request data-hash has been matched with the action data-hash", f)

	return nil
}

func (task *CascadeRegistrationTask) encodeInput(ctx context.Context, actionID string, path string, dataSize int, f logtrace.Fields) (*adaptors.EncodeResult, error) {
	resp, err := task.RQ.EncodeInput(ctx, actionID, path, dataSize)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to encode data", err, f)
	}
	return &resp, nil
}

func (task *CascadeRegistrationTask) verifySignatureAndDecodeLayout(ctx context.Context, encoded string, creator string,
	encodedMeta codec.Layout, f logtrace.Fields) (codec.Layout, string, error) {

	// Extract index file and creator signature from encoded data
	// The signatures field contains: Base64(index_file).creators_signature
	indexFileB64, creatorSig, err := extractIndexFileAndSignature(encoded)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to extract index file and creator signature", err, f)
	}

	// Verify creator signature on index file
	creatorSigBytes, err := base64.StdEncoding.DecodeString(creatorSig)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode creator signature from base64", err, f)
	}

	if err := task.LumeraClient.Verify(ctx, creator, []byte(indexFileB64), creatorSigBytes); err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to verify creator signature", err, f)
	}
	logtrace.Info(ctx, "creator signature successfully verified", f)

	// Decode index file to get the layout signature
	indexFile, err := decodeIndexFile(indexFileB64)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode index file", err, f)
	}

	// Verify layout signature on the actual layout
	layoutSigBytes, err := base64.StdEncoding.DecodeString(indexFile.LayoutSignature)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode layout signature from base64", err, f)
	}

	layoutJSON, err := json.Marshal(encodedMeta)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to marshal layout", err, f)
	}
	layoutB64 := utils.B64Encode(layoutJSON)
	if err := task.LumeraClient.Verify(ctx, creator, layoutB64, layoutSigBytes); err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to verify layout signature", err, f)
	}
	logtrace.Info(ctx, "layout signature successfully verified", f)

	return encodedMeta, indexFile.LayoutSignature, nil
}

func (task *CascadeRegistrationTask) generateRQIDFiles(ctx context.Context, meta actiontypes.CascadeMetadata,
	sig, creator string, encodedMeta codec.Layout, f logtrace.Fields) (GenRQIdentifiersFilesResponse, error) {
	// The signatures field contains: Base64(index_file).creators_signature
	// This full format will be used for ID generation to match chain expectations

	// Generate layout files
	layoutRes, err := GenRQIdentifiersFiles(ctx, GenRQIdentifiersFilesRequest{
		Metadata:         encodedMeta,
		CreatorSNAddress: creator,
		RqMax:            uint32(meta.RqIdsMax),
		Signature:        sig,
		IC:               uint32(meta.RqIdsIc),
	})
	if err != nil {
		return GenRQIdentifiersFilesResponse{},
			task.wrapErr(ctx, "failed to generate layout files", err, f)
	}

	// Generate index files using full signatures format for ID generation (matches chain expectation)
	indexIDs, indexFiles, err := GenIndexFiles(ctx, layoutRes.RedundantMetadataFiles, sig, meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	if err != nil {
		return GenRQIdentifiersFilesResponse{},
			task.wrapErr(ctx, "failed to generate index files", err, f)
	}

	// Store layout files and index files separately in P2P
	allFiles := append(layoutRes.RedundantMetadataFiles, indexFiles...)

	// Return index IDs (sent to chain) and all files (stored in P2P)
	return GenRQIdentifiersFilesResponse{
		RQIDs:                  indexIDs,
		RedundantMetadataFiles: allFiles,
	}, nil
}

// storeArtefacts persists cascade artefacts (ID files + RaptorQ symbols) via the
// P2P adaptor and returns an aggregated network success rate percentage and total
// node requests used to compute it.
//
// Aggregation details:
//   - Underlying batches return (ratePct, requests) where `requests` is the number
//     of node RPCs attempted. The adaptor computes a weighted average by requests
//     across all batches, reflecting the overall network success rate.
func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, actionID string, idFiles [][]byte, symbolsDir string, f logtrace.Fields) (adaptors.StoreArtefactsMetrics, error) {
	return task.P2P.StoreArtefacts(ctx, adaptors.StoreArtefactsRequest{
		IDFiles:    idFiles,
		SymbolsDir: symbolsDir,
		TaskID:     task.ID(),
		ActionID:   actionID,
	}, f)
}

func (task *CascadeRegistrationTask) wrapErr(ctx context.Context, msg string, err error, f logtrace.Fields) error {
	if err != nil {
		f[logtrace.FieldError] = err.Error()
	}
	logtrace.Error(ctx, msg, f)

	// Preserve the root cause in the gRPC error description so callers receive full context.
	if err != nil {
		return status.Errorf(codes.Internal, "%s: %v", msg, err)
	}
	return status.Errorf(codes.Internal, "%s", msg)
}

// emitArtefactsStored builds a single-line metrics summary and emits the
// SupernodeEventTypeArtefactsStored event while logging the metrics line.
func (task *CascadeRegistrationTask) emitArtefactsStored(
	ctx context.Context,
	metrics adaptors.StoreArtefactsMetrics,
	fields logtrace.Fields,
	layout codec.Layout,
	send func(resp *RegisterResponse) error,
) {
	if fields == nil {
		fields = logtrace.Fields{}
	}

	// Layout and symbol stats (no mixing of metadata files with symbol chunks)
	layoutSymbolsTotal, layoutMinRequiredSymbols, layoutMinRequiredPct := computeLayoutStats(layout)
	storedTotal := metrics.SymCount
	missingTotal := 0
	if layoutSymbolsTotal > storedTotal {
		missingTotal = layoutSymbolsTotal - storedTotal
	}
	firstPassTargetPct := 18.0
	firstPassAchievedPct := 0.0
	if layoutSymbolsTotal > 0 {
		firstPassAchievedPct = (float64(storedTotal) / float64(layoutSymbolsTotal)) * 100.0
	}

	// Aggregate nodes for metadata and symbols
	metaNodes, metaNodesTotal := aggregateStoreCalls(metrics.MetaCalls, metrics.MetaCount)
	symNodes, symNodesTotal := aggregateStoreCalls(metrics.SymCalls, storedTotal)

	// Build new, compact payload with separate sections for layout, metadata, symbols and network
	payload := map[string]any{
		"layout": map[string]any{
			"symbols_total":        layoutSymbolsTotal,
			"min_required_symbols": layoutMinRequiredSymbols,
			"min_required_pct":     layoutMinRequiredPct,
		},
		"metadata": map[string]any{
			"files_total":      metrics.MetaCount,
			"success_rate_pct": metrics.MetaRate,
			"requests":         metrics.MetaRequests,
			"duration_ms":      metrics.MetaDurationMS,
			"nodes_total":      metaNodesTotal,
			"nodes":            metaNodes,
		},
		"symbols": map[string]any{
			"stored_total":            storedTotal,
			"missing_total":           missingTotal,
			"first_pass_target_pct":   firstPassTargetPct,
			"first_pass_achieved_pct": firstPassAchievedPct,
			"success_rate_pct":        metrics.SymRate,
			"requests":                metrics.SymRequests,
			"duration_ms":             metrics.SymDurationMS,
			"nodes_total":             symNodesTotal,
			"nodes":                   symNodes,
		},
		"network": map[string]any{
			"success_rate_pct": metrics.AggregatedRate,
			"total_requests":   metrics.TotalRequests,
		},
	}

	b, _ := json.MarshalIndent(payload, "", "  ")
	msg := string(b)
	fields["metrics_json"] = msg
	logtrace.Info(ctx, "artefacts have been stored", fields)
	task.streamEvent(SupernodeEventTypeArtefactsStored, msg, "", send)
}

// extractSignatureAndFirstPart extracts the signature and first part from the encoded data
// data is expected to be in format: b64(JSON(Layout)).Signature
func extractSignatureAndFirstPart(data string) (encodedMetadata string, signature string, err error) {
	parts := strings.Split(data, ".")
	if len(parts) < 2 {
		return "", "", errors.New("invalid data format")
	}

	// The first part is the base64 encoded data
	return parts[0], parts[1], nil
}

func decodeMetadataFile(data string) (layout codec.Layout, err error) {
	// Decode the base64 encoded data
	decodedData, err := utils.B64Decode([]byte(data))
	if err != nil {
		return layout, errors.Errorf("failed to decode data: %w", err)
	}

	// Unmarshal the decoded data into a layout
	if err := json.Unmarshal(decodedData, &layout); err != nil {
		return layout, errors.Errorf("failed to unmarshal data: %w", err)
	}

	return layout, nil
}

func verifyIDs(ticketMetadata, metadata codec.Layout) error {
	// Verify that the symbol identifiers match between versions
	if err := utils.EqualStrList(ticketMetadata.Blocks[0].Symbols, metadata.Blocks[0].Symbols); err != nil {
		return errors.Errorf("symbol identifiers don't match: %w", err)
	}

	// Verify that the block hashes match
	if ticketMetadata.Blocks[0].Hash != metadata.Blocks[0].Hash {
		return errors.New("block hashes don't match")
	}

	return nil
}

// verifyActionFee checks if the action fee is sufficient for the given data size
// It fetches action parameters, calculates the required fee, and compares it with the action price
func (task *CascadeRegistrationTask) verifyActionFee(ctx context.Context, action *actiontypes.Action, dataSize int, fields logtrace.Fields) error {
	dataSizeInKBs := dataSize / 1024
	fee, err := task.LumeraClient.GetActionFee(ctx, strconv.Itoa(dataSizeInKBs))
	if err != nil {
		return task.wrapErr(ctx, "failed to get action fee", err, fields)
	}

	// Parse fee amount from string to int64
	amount, err := strconv.ParseInt(fee.Amount, 10, 64)
	if err != nil {
		return task.wrapErr(ctx, "failed to parse fee amount", err, fields)
	}

	// Calculate per-byte fee based on data size
	requiredFee := sdk.NewCoin("ulume", math.NewInt(amount))

	// Log the calculated fee
	logtrace.Info(ctx, "calculated required fee", logtrace.Fields{
		"fee":       requiredFee.String(),
		"dataBytes": dataSize,
	})
	// Check if action price is less than required fee
	if action.Price.IsLT(requiredFee) {
		return task.wrapErr(
			ctx,
			"insufficient fee",
			fmt.Errorf("expected at least %s, got %s", requiredFee.String(), action.Price.String()),
			fields,
		)
	}

	return nil
}

func parseRQMetadataFile(data []byte) (layout codec.Layout, signature string, counter string, err error) {
	decompressed, err := utils.ZstdDecompress(data)
	if err != nil {
		return layout, "", "", errors.Errorf("decompress rq metadata file: %w", err)
	}

	// base64EncodeMetadata.Signature.Counter
	parts := bytes.Split(decompressed, []byte{SeparatorByte})
	if len(parts) != 3 {
		return layout, "", "", errors.New("invalid rq metadata format: expecting 3 parts (layout, signature, counter)")
	}

	layoutJson, err := utils.B64Decode(parts[0])
	if err != nil {
		return layout, "", "", errors.Errorf("base64 decode failed: %w", err)
	}

	if err := json.Unmarshal(layoutJson, &layout); err != nil {
		return layout, "", "", errors.Errorf("unmarshal layout: %w", err)
	}

	signature = string(parts[1])
	counter = string(parts[2])

	return layout, signature, counter, nil
}

// extractIndexFileAndSignature extracts index file and creator signature from signatures field
// data is expected to be in format: Base64(index_file).creators_signature
func extractIndexFileAndSignature(data string) (indexFileB64 string, creatorSignature string, err error) {
	parts := strings.Split(data, ".")
	if len(parts) < 2 {
		return "", "", errors.New("invalid signatures format")
	}
	return parts[0], parts[1], nil
}

// decodeIndexFile decodes base64 encoded index file
func decodeIndexFile(data string) (IndexFile, error) {
	var indexFile IndexFile
	decodedData, err := utils.B64Decode([]byte(data))
	if err != nil {
		return indexFile, errors.Errorf("failed to decode index file: %w", err)
	}
	if err := json.Unmarshal(decodedData, &indexFile); err != nil {
		return indexFile, errors.Errorf("failed to unmarshal index file: %w", err)
	}
	return indexFile, nil
}

// VerifyDownloadSignature verifies the download signature for actionID.creatorAddress
func (task *CascadeRegistrationTask) VerifyDownloadSignature(ctx context.Context, actionID, signature string) error {
	fields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
		logtrace.FieldMethod:   "VerifyDownloadSignature",
	}

	// Get action details to extract creator address
	actionDetails, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}

	creatorAddress := actionDetails.GetAction().Creator
	fields["creator_address"] = creatorAddress

	// Create the expected signature data: actionID.creatorAddress
	signatureData := fmt.Sprintf("%s.%s", actionID, creatorAddress)
	fields["signature_data"] = signatureData

	// Decode the base64 signature
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return task.wrapErr(ctx, "failed to decode signature from base64", err, fields)
	}

	// Verify the signature using Lumera client
	if err := task.LumeraClient.Verify(ctx, creatorAddress, []byte(signatureData), signatureBytes); err != nil {
		return task.wrapErr(ctx, "failed to verify download signature", err, fields)
	}

	logtrace.Info(ctx, "download signature successfully verified", fields)
	return nil
}
