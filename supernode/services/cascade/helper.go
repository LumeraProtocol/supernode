package cascade

import (
    "context"
    "encoding/base64"
    "fmt"
    "strconv"

    "cosmossdk.io/math"
    actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
    "github.com/LumeraProtocol/supernode/v2/pkg/codec"
    "github.com/LumeraProtocol/supernode/v2/pkg/errors"
    "github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
    "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
    "github.com/LumeraProtocol/supernode/v2/pkg/utils"
    "github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
    "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"

    sdk "github.com/cosmos/cosmos-sdk/types"
    json "github.com/json-iterator/go"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

// layout stats helpers removed to keep download metrics minimal.

func (task *CascadeRegistrationTask) fetchAction(ctx context.Context, actionID string, f logtrace.Fields) (*actiontypes.Action, error) {
	res, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to get action", err, f)
	}

	if res.GetAction().ActionID == "" {
		return nil, task.wrapErr(ctx, "action not found", errors.New(""), f)
	}
	logtrace.Debug(ctx, "action has been retrieved", f)

	return res.GetAction(), nil
}

func (task *CascadeRegistrationTask) ensureIsTopSupernode(ctx context.Context, blockHeight uint64, f logtrace.Fields) error {
	top, err := task.LumeraClient.GetTopSupernodes(ctx, blockHeight)
	if err != nil {
		return task.wrapErr(ctx, "failed to get top SNs", err, f)
	}
	logtrace.Debug(ctx, "Fetched Top Supernodes", f)

	if !supernode.Exists(top.Supernodes, task.config.SupernodeAccountAddress) {
		// Build information about supernodes for better error context
		addresses := make([]string, len(top.Supernodes))
		for i, sn := range top.Supernodes {
			addresses[i] = sn.SupernodeAccount
		}
		logtrace.Debug(ctx, "Supernode not in top list", logtrace.Fields{
			"currentAddress": task.config.SupernodeAccountAddress,
			"topSupernodes":  addresses,
		})
		return task.wrapErr(ctx, "current supernode does not exist in the top SNs list",
			errors.Errorf("current address: %s, top supernodes: %v", task.config.SupernodeAccountAddress, addresses), f)
	}

	return nil
}

// decodeCascadeMetadata moved to cascadekit.UnmarshalCascadeMetadata
// verifyDataHash moved to cascadekit.VerifyB64DataHash

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
    indexFileB64, creatorSig, err := cascadekit.ExtractIndexAndCreatorSig(encoded)
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
	logtrace.Debug(ctx, "creator signature successfully verified", f)

    // Decode index file to get the layout signature
    indexFile, err := cascadekit.DecodeIndexB64(indexFileB64)
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
	logtrace.Debug(ctx, "layout signature successfully verified", f)

    return encodedMeta, indexFile.LayoutSignature, nil
}

func (task *CascadeRegistrationTask) generateRQIDFiles(ctx context.Context, meta actiontypes.CascadeMetadata,
    sig, creator string, encodedMeta codec.Layout, f logtrace.Fields) (cascadekit.GenRQIdentifiersFilesResponse, error) {
    // The signatures field contains: Base64(index_file).creators_signature
    // This full format will be used for ID generation to match chain expectations

    // Generate layout files (redundant metadata files)
    layoutRes, err := cascadekit.GenerateLayoutFiles(ctx, encodedMeta, sig, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
    if err != nil {
        return cascadekit.GenRQIdentifiersFilesResponse{},
            task.wrapErr(ctx, "failed to generate layout files", err, f)
    }

    // Generate index files using full signatures format for ID generation (matches chain expectation)
    indexIDs, indexFiles, err := cascadekit.GenerateIndexFiles(ctx, meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
    if err != nil {
        return cascadekit.GenRQIdentifiersFilesResponse{},
            task.wrapErr(ctx, "failed to generate index files", err, f)
    }

    // Store layout files and index files separately in P2P
    allFiles := append(layoutRes.RedundantMetadataFiles, indexFiles...)

    // Return index IDs (sent to chain) and all files (stored in P2P)
    return cascadekit.GenRQIdentifiersFilesResponse{
        RQIDs:                  indexIDs,
        RedundantMetadataFiles: allFiles,
    }, nil
}

// storeArtefacts persists cascade artefacts (ID files + RaptorQ symbols) via the
// P2P adaptor. P2P does not return metrics; cascade summarizes and emits them.
func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, actionID string, idFiles [][]byte, symbolsDir string, f logtrace.Fields) error {
	if f == nil {
		f = logtrace.Fields{}
	}
	lf := logtrace.Fields{
		logtrace.FieldActionID: actionID,
		logtrace.FieldTaskID:   task.ID(),
		"id_files_count":       len(idFiles),
		"symbols_dir":          symbolsDir,
	}
	for k, v := range f {
		lf[k] = v
	}
	// Tag the flow as first-pass just before handing over to P2P
	ctx = logtrace.CtxWithOrigin(ctx, "first_pass")
	logtrace.Info(ctx, "store: first-pass begin", lf)

	if err := task.P2P.StoreArtefacts(ctx, adaptors.StoreArtefactsRequest{
		IDFiles:    idFiles,
		SymbolsDir: symbolsDir,
		TaskID:     task.ID(),
		ActionID:   actionID,
	}, f); err != nil {
		// Log and wrap to ensure a proper error line and context
		return task.wrapErr(ctx, "failed to store artefacts", err, lf)
	}
	return nil
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
	fields logtrace.Fields,
	_ codec.Layout,
	send func(resp *RegisterResponse) error,
) {
	if fields == nil {
		fields = logtrace.Fields{}
	}

	// Emit a minimal event message (metrics system removed)
	msg := "Artefacts stored"
	logtrace.Debug(ctx, "artefacts have been stored", fields)
	task.streamEvent(SupernodeEventTypeArtefactsStored, msg, "", send)
}

// Removed legacy helpers; functionality is centralized in cascadekit.

//

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
	logtrace.Debug(ctx, "calculated required fee", logtrace.Fields{
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

//

//

//

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

    // Create the expected signature data: actionID (creator address not included in payload)
    signatureData := fmt.Sprintf("%s", actionID)
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

	logtrace.Debug(ctx, "download signature successfully verified", fields)
	return nil
}
