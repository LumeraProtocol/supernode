package cascade

import (
	"context"
	"os"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
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

// Register processes the upload request for cascade input data.
// 1- Fetch & validate action (it should be a cascade action registered on the chain)
// 2- Ensure this super-node is eligible to process the action (should be in the top supernodes list for the action block height)
// 3- Get the cascade metadata from the action: it contains the data hash and the signatures
//
//	Assuming data hash is a base64 encoded string of blake3 hash of the data
//	The signatures field is: b64(JSON(Layout)).Signature where Layout is codec.Layout
//	The layout is a JSON object that contains the metadata of the data
//
// 4- Verify the data hash (the data hash should match the one in the action ticket) - again, hash function should be blake3
// 5- Generate Symbols with codec (RQ-Go Library) (the data should be encoded using the codec)
// 6- Extract the layout and the signature from Step 3. Verify the signature using the creator's public key (creator address is in the action)
// 7- Generate RQ-ID files from the layout that we generated locally and then match those with the ones in the action
// 8- Verify the IDs in the layout and the metadata (the IDs should match the ones in the action)
// 9- Store the artefacts in P2P Storage (the redundant metadata files and the symbols from the symbols dir)
func (task *CascadeRegistrationTask) Register(
	ctx context.Context,
	req *RegisterRequest,
	send func(resp *RegisterResponse) error,
) (err error) {
	// Seed correlation ID and origin so logs across layers can be joined and filtered
	if req != nil && req.ActionID != "" {
		ctx = logtrace.CtxWithCorrelationID(ctx, req.ActionID)
		ctx = logtrace.CtxWithOrigin(ctx, "first_pass")
	}

	fields := logtrace.Fields{logtrace.FieldMethod: "Register", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "register: request", fields)

	// Ensure task status and resources are finalized regardless of outcome
	defer func() {
		if err != nil {
			task.UpdateStatus(common.StatusTaskCanceled)
		} else {
			task.UpdateStatus(common.StatusTaskCompleted)
		}
		task.Cancel()
	}()

	// Always attempt to remove the uploaded file path
	defer func() {
		if req != nil && req.FilePath != "" {
			if remErr := os.RemoveAll(req.FilePath); remErr != nil {
				logtrace.Warn(ctx, "Failed to remove uploaded file", fields)
			} else {
				logtrace.Debug(ctx, "Uploaded file cleaned up", fields)
			}
		}
	}()

	/* 1. Fetch & validate action -------------------------------------------------- */
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

	/* 2. Verify action fee -------------------------------------------------------- */
	if err := task.verifyActionFee(ctx, action, req.DataSize, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: fee verified", fields)
	task.streamEvent(SupernodeEventTypeActionFeeVerified, "Action fee verified", "", send)

	/* 3. Ensure this super-node is eligible -------------------------------------- */
	fields[logtrace.FieldSupernodeState] = task.config.SupernodeAccountAddress
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: top supernode confirmed", fields)
	task.streamEvent(SupernodeEventTypeTopSupernodeCheckPassed, "Top supernode eligibility confirmed", "", send)

	/* 4. Decode cascade metadata -------------------------------------------------- */
	cascadeMeta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		return task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "register: metadata decoded", fields)
	task.streamEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", send)

	/* 5. Verify data hash --------------------------------------------------------- */
	if err := cascadekit.VerifyB64DataHash(req.DataHash, cascadeMeta.DataHash); err != nil {
		return err
	}
	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "register: data hash matched", fields)
	task.streamEvent(SupernodeEventTypeDataHashVerified, "Data hash verified", "", send)

	/* 6. Encode the raw data ------------------------------------------------------ */
	encResp, err := task.encodeInput(ctx, req.ActionID, req.FilePath, req.DataSize, fields)
	if err != nil {
		return err
	}
	// Promote to Info and include symbols directory for quick visibility
	fields["symbols_dir"] = encResp.SymbolsDir
	logtrace.Info(ctx, "register: input encoded", fields)
	task.streamEvent(SupernodeEventTypeInputEncoded, "Input encoded", "", send)

	/* 7. Signature verification + layout decode ---------------------------------- */
	layout, signature, err := task.verifySignatureAndDecodeLayout(
		ctx, cascadeMeta.Signatures, action.Creator, encResp.Metadata, fields,
	)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "register: signature verified", fields)
	task.streamEvent(SupernodeEventTypeSignatureVerified, "Signature verified", "", send)

	/* 8. Generate RQ-ID files ----------------------------------------------------- */
	rqidResp, err := task.generateRQIDFiles(ctx, cascadeMeta, signature, action.Creator, encResp.Metadata, fields)
	if err != nil {
		return err
	}
	// Include count of ID files generated for visibility
	fields["id_files_count"] = len(rqidResp.RedundantMetadataFiles)
	logtrace.Info(ctx, "register: rqid files generated", fields)
	task.streamEvent(SupernodeEventTypeRQIDsGenerated, "RQID files generated", "", send)

	/* 9. Consistency checks ------------------------------------------------------- */
	if err := cascadekit.VerifySingleBlockIDs(layout, encResp.Metadata); err != nil {
		return task.wrapErr(ctx, "failed to verify IDs", err, fields)
	}
	logtrace.Info(ctx, "register: rqids validated", fields)
	task.streamEvent(SupernodeEventTypeRqIDsVerified, "RQIDs verified", "", send)

	/* 10. Simulate finalize to avoid storing artefacts if it would fail ---------- */
	if _, err := task.LumeraClient.SimulateFinalizeAction(ctx, action.ActionID, rqidResp.RQIDs); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "register: finalize simulation failed", fields)
		// Emit explicit simulation failure event for client visibility
		task.streamEvent(SupernodeEventTypeFinalizeSimulationFailed, "Finalize simulation failed", "", send)
		return task.wrapErr(ctx, "finalize action simulation failed", err, fields)
	}
	logtrace.Info(ctx, "register: finalize simulation passed", fields)
	// Transmit as a standard event so SDK can propagate it (dedicated type)
	task.streamEvent(SupernodeEventTypeFinalizeSimulated, "Finalize simulation passed", "", send)

	/* 11. Persist artefacts -------------------------------------------------------- */
	// Persist artefacts to the P2P network. P2P interfaces return error only;
	// metrics are summarized at the cascade layer and emitted via event.
	if err := task.storeArtefacts(ctx, action.ActionID, rqidResp.RedundantMetadataFiles, encResp.SymbolsDir, fields); err != nil {
		return err
	}
	// Emit artefacts stored event (metrics payload removed; logs preserved)
	task.emitArtefactsStored(ctx, fields, encResp.Metadata, send)

	resp, err := task.LumeraClient.FinalizeAction(ctx, action.ActionID, rqidResp.RQIDs)
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
