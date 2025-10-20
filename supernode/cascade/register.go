package cascade

import (
	"context"
	"os"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// RegisterRequest contains parameters for upload request
type RegisterRequest struct {
	TaskID   string
	ActionID string
	DataHash []byte
	DataSize int
	FilePath string
}

// RegisterResponse contains the result of upload
type RegisterResponse struct {
	EventType SupernodeEventType
	Message   string
	TxHash    string
}

func (task *CascadeRegistrationTask) Register(
	ctx context.Context,
	req *RegisterRequest,
	send func(resp *RegisterResponse) error,
) (err error) {
	// Step 1: Correlate context and capture task identity
	if req != nil && req.ActionID != "" {
		ctx = logtrace.CtxWithCorrelationID(ctx, req.ActionID)
		ctx = logtrace.CtxWithOrigin(ctx, "first_pass")
		task.taskID = req.TaskID
	}

	// Step 2: Log request and ensure uploaded file cleanup
	fields := logtrace.Fields{logtrace.FieldMethod: "Register", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "register: request", fields)
	defer func() {
		if req != nil && req.FilePath != "" {
			if remErr := os.RemoveAll(req.FilePath); remErr != nil {
				logtrace.Warn(ctx, "Failed to remove uploaded file", fields)
			} else {
				logtrace.Debug(ctx, "Uploaded file cleaned up", fields)
			}
		}
	}()

	// Step 3: Fetch the action details
	action, err := task.fetchAction(ctx, req.ActionID, fields)
	if err != nil {
		return err
	}
	fields[logtrace.FieldBlockHeight] = action.BlockHeight
	fields[logtrace.FieldCreator] = action.Creator
	fields[logtrace.FieldStatus] = action.State
	fields[logtrace.FieldPrice] = action.Price
	logtrace.Info(ctx, "register: action fetched", fields)
	task.streamEvent(SupernodeEventTypeActionRetrieved, "Action retrieved", "", send)

	// Step 4: Verify action fee based on data size (rounded up to KB)
	if err := task.verifyActionFee(ctx, action, req.DataSize, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: fee verified", fields)
	task.streamEvent(SupernodeEventTypeActionFeeVerified, "Action fee verified", "", send)

	// Step 5: Ensure this node is eligible (top supernode for block)
	fields[logtrace.FieldSupernodeState] = task.SupernodeAccountAddress
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: top supernode confirmed", fields)
	task.streamEvent(SupernodeEventTypeTopSupernodeCheckPassed, "Top supernode eligibility confirmed", "", send)

	// Step 6: Decode Cascade metadata from the action
	cascadeMeta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		return task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "register: metadata decoded", fields)
	task.streamEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", send)

	// Step 7: Verify request-provided data hash matches metadata
	if err := cascadekit.VerifyB64DataHash(req.DataHash, cascadeMeta.DataHash); err != nil {
		return err
	}
	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "register: data hash matched", fields)
	task.streamEvent(SupernodeEventTypeDataHashVerified, "Data hash verified", "", send)

	// Step 8: Encode input using the RQ codec to produce layout and symbols
	encodeResult, err := task.encodeInput(ctx, req.ActionID, req.FilePath, fields)
	if err != nil {
		return err
	}
	fields["symbols_dir"] = encodeResult.SymbolsDir
	logtrace.Info(ctx, "register: input encoded", fields)
	task.streamEvent(SupernodeEventTypeInputEncoded, "Input encoded", "", send)

	// Step 9: Verify index and layout signatures; produce layoutB64
	logtrace.Info(ctx, "register: verify+decode layout start", fields)
	indexFile, layoutB64, vErr := task.validateIndexAndLayout(ctx, action.Creator, cascadeMeta.Signatures, encodeResult.Layout)
	if vErr != nil {
		return task.wrapErr(ctx, "signature or index validation failed", vErr, fields)
	}
	layoutSignatureB64 := indexFile.LayoutSignature
	logtrace.Info(ctx, "register: signature verified", fields)
	task.streamEvent(SupernodeEventTypeSignatureVerified, "Signature verified", "", send)

	// Step 10: Generate RQID files (layout and index) and compute IDs
	rqIDs, idFiles, err := task.generateRQIDFiles(ctx, cascadeMeta, layoutSignatureB64, layoutB64, fields)
	if err != nil {
		return err
	}

	// Calculate combined size of all index and layout files
	totalSize := 0
	for _, file := range idFiles {
		totalSize += len(file)
	}

	fields["id_files_count"] = len(idFiles)
	fields["rqids_count"] = len(rqIDs)
	fields["combined_files_size_bytes"] = totalSize
	fields["combined_files_size_kb"] = float64(totalSize) / 1024
	fields["combined_files_size_mb"] = float64(totalSize) / (1024 * 1024)
	logtrace.Info(ctx, "register: rqid files generated", fields)
	task.streamEvent(SupernodeEventTypeRQIDsGenerated, "RQID files generated", "", send)

	logtrace.Info(ctx, "register: rqids validated", fields)
	task.streamEvent(SupernodeEventTypeRqIDsVerified, "RQIDs verified", "", send)

	// Step 11: Simulate finalize to ensure the tx will succeed
	if _, err := task.LumeraClient.SimulateFinalizeAction(ctx, action.ActionID, rqIDs); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "register: finalize simulation failed", fields)
		task.streamEvent(SupernodeEventTypeFinalizeSimulationFailed, "Finalize simulation failed", "", send)
		return task.wrapErr(ctx, "finalize action simulation failed", err, fields)
	}
	logtrace.Info(ctx, "register: finalize simulation passed", fields)
	task.streamEvent(SupernodeEventTypeFinalizeSimulated, "Finalize simulation passed", "", send)

	// Step 12: Store artefacts to the network store
	if err := task.storeArtefacts(ctx, action.ActionID, idFiles, encodeResult.SymbolsDir, fields); err != nil {
		return err
	}
	task.emitArtefactsStored(ctx, fields, encodeResult.Layout, send)

	// Step 13: Finalize the action on-chain
	resp, err := task.LumeraClient.FinalizeAction(ctx, action.ActionID, rqIDs)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "register: finalize action error", fields)
		return task.wrapErr(ctx, "failed to finalize action", err, fields)
	}
	txHash := resp.TxResponse.TxHash
	fields[logtrace.FieldTxHash] = txHash
	logtrace.Info(ctx, "register: action finalized", fields)
	task.streamEvent(SupernodeEventTypeActionFinalized, "Action finalized", txHash, send)
	return nil
}
