package cascade

import (
	"context"
	"os"

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

// RegisterTimeout bounds the execution time of a Register task to prevent tasks
// from lingering in a non-final state if a dependency stalls. Keep greater than
// the SDK's upload+processing budgets so the client cancels first.

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
	// Defensive envelope deadline to guarantee task finalization
	ctx, cancel := context.WithTimeout(ctx, RegisterTimeout)
	defer cancel()

	fields := logtrace.Fields{logtrace.FieldMethod: "Register", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "Cascade registration request received", fields)

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
				logtrace.Info(ctx, "Uploaded file cleaned up", fields)
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
	logtrace.Info(ctx, "Action retrieved", fields)
	task.streamEvent(SupernodeEventTypeActionRetrieved, "Action retrieved", "", send)

	/* 2. Verify action fee -------------------------------------------------------- */
	if err := task.verifyActionFee(ctx, action, req.DataSize, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "Action fee verified", fields)
	task.streamEvent(SupernodeEventTypeActionFeeVerified, "Action fee verified", "", send)

	/* 3. Ensure this super-node is eligible -------------------------------------- */
	fields[logtrace.FieldSupernodeState] = task.config.SupernodeAccountAddress
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "Top supernode eligibility confirmed", fields)
	task.streamEvent(SupernodeEventTypeTopSupernodeCheckPassed, "Top supernode eligibility confirmed", "", send)

	/* 4. Decode cascade metadata -------------------------------------------------- */
	cascadeMeta, err := task.decodeCascadeMetadata(ctx, action.Metadata, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "Cascade metadata decoded", fields)
	task.streamEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", send)

	/* 5. Verify data hash --------------------------------------------------------- */
	if err := task.verifyDataHash(ctx, req.DataHash, cascadeMeta.DataHash, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "Data hash verified", fields)
	task.streamEvent(SupernodeEventTypeDataHashVerified, "Data hash verified", "", send)

	/* 6. Encode the raw data ------------------------------------------------------ */
	encResp, err := task.encodeInput(ctx, req.ActionID, req.FilePath, req.DataSize, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "Input encoded", fields)
	task.streamEvent(SupernodeEventTypeInputEncoded, "Input encoded", "", send)

	/* 7. Signature verification + layout decode ---------------------------------- */
	layout, signature, err := task.verifySignatureAndDecodeLayout(
		ctx, cascadeMeta.Signatures, action.Creator, encResp.Metadata, fields,
	)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "Signature verified", fields)
	task.streamEvent(SupernodeEventTypeSignatureVerified, "Signature verified", "", send)

	/* 8. Generate RQ-ID files ----------------------------------------------------- */
	rqidResp, err := task.generateRQIDFiles(ctx, cascadeMeta, signature, action.Creator, encResp.Metadata, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "RQID files generated", fields)
	task.streamEvent(SupernodeEventTypeRQIDsGenerated, "RQID files generated", "", send)

	/* 9. Consistency checks ------------------------------------------------------- */
	if err := verifyIDs(layout, encResp.Metadata); err != nil {
		return task.wrapErr(ctx, "failed to verify IDs", err, fields)
	}
	logtrace.Info(ctx, "RQIDs verified", fields)
	task.streamEvent(SupernodeEventTypeRqIDsVerified, "RQIDs verified", "", send)

	/* 10. Simulate finalize to avoid storing artefacts if it would fail ---------- */
	if _, err := task.LumeraClient.SimulateFinalizeAction(ctx, action.ActionID, rqidResp.RQIDs); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "Finalize simulation failed", fields)
		// Emit explicit simulation failure event for client visibility
		task.streamEvent(SupernodeEventTypeFinalizeSimulationFailed, "Finalize simulation failed", "", send)
		return task.wrapErr(ctx, "finalize action simulation failed", err, fields)
	}
	logtrace.Info(ctx, "Finalize simulation passed", fields)
	// Transmit as a standard event so SDK can propagate it (dedicated type)
	task.streamEvent(SupernodeEventTypeFinalizeSimulated, "Finalize simulation passed", "", send)

	/* 11. Persist artefacts -------------------------------------------------------- */
	// Persist artefacts to the P2P network.
	// Aggregation model (context):
	// - Each underlying StoreBatch returns (ratePct, requests) where requests is
	//   the number of node RPCs. The aggregated success rate can be computed as a
	//   weighted average by requests across metadata and symbol batches, yielding
	//   an overall network success view for the action.
	metrics, err := task.storeArtefacts(ctx, action.ActionID, rqidResp.RedundantMetadataFiles, encResp.SymbolsDir, fields)
	if err != nil {
		return err
	}
	// Emit single-line metrics via helper to keep Register clean
	task.emitArtefactsStored(ctx, metrics, fields, send)

	resp, err := task.LumeraClient.FinalizeAction(ctx, action.ActionID, rqidResp.RQIDs)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "Finalize action error", fields)
		return task.wrapErr(ctx, "failed to finalize action", err, fields)
	}
	txHash := resp.TxResponse.TxHash
	fields[logtrace.FieldTxHash] = txHash
	logtrace.Info(ctx, "Action finalized", fields)
	task.streamEvent(SupernodeEventTypeActionFinalized, "Action finalized", txHash, send)

	return nil
}
