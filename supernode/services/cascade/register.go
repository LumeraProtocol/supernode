package cascade

import (
    "context"
    "fmt"
    "math"
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

	fields := logtrace.Fields{logtrace.FieldMethod: "Register", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "cascade-action-registration request received", fields)

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
				logtrace.Warn(ctx, "error removing file", fields)
			} else {
				logtrace.Info(ctx, "input file has been cleaned up", fields)
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
	logtrace.Info(ctx, "action has been retrieved", fields)
	task.streamEvent(SupernodeEventTypeActionRetrieved, "action has been retrieved", "", send)

	/* 2. Verify action fee -------------------------------------------------------- */
	if err := task.verifyActionFee(ctx, action, req.DataSize, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "action fee has been validated", fields)
	task.streamEvent(SupernodeEventTypeActionFeeVerified, "action-fee has been validated", "", send)

	/* 3. Ensure this super-node is eligible -------------------------------------- */
	fields[logtrace.FieldSupernodeState] = task.config.SupernodeAccountAddress
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "current-supernode exists in the top-sn list", fields)
	task.streamEvent(SupernodeEventTypeTopSupernodeCheckPassed, "current supernode exists in the top-sn list", "", send)

	/* 4. Decode cascade metadata -------------------------------------------------- */
	cascadeMeta, err := task.decodeCascadeMetadata(ctx, action.Metadata, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "cascade metadata decoded", fields)
	task.streamEvent(SupernodeEventTypeMetadataDecoded, "cascade metadata has been decoded", "", send)

	/* 5. Verify data hash --------------------------------------------------------- */
	if err := task.verifyDataHash(ctx, req.DataHash, cascadeMeta.DataHash, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "data-hash has been verified", fields)
	task.streamEvent(SupernodeEventTypeDataHashVerified, "data-hash has been verified", "", send)

	/* 6. Encode the raw data ------------------------------------------------------ */
	encResp, err := task.encodeInput(ctx, req.ActionID, req.FilePath, req.DataSize, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "input-data has been encoded", fields)
	task.streamEvent(SupernodeEventTypeInputEncoded, "input data has been encoded", "", send)

	/* 7. Signature verification + layout decode ---------------------------------- */
	layout, signature, err := task.verifySignatureAndDecodeLayout(
		ctx, cascadeMeta.Signatures, action.Creator, encResp.Metadata, fields,
	)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "signature has been verified", fields)
	task.streamEvent(SupernodeEventTypeSignatureVerified, "signature has been verified", "", send)

	/* 8. Generate RQ-ID files ----------------------------------------------------- */
	rqidResp, err := task.generateRQIDFiles(ctx, cascadeMeta, signature, action.Creator, encResp.Metadata, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "rq-id files have been generated", fields)
	task.streamEvent(SupernodeEventTypeRQIDsGenerated, "rq-id files have been generated", "", send)

	/* 9. Consistency checks ------------------------------------------------------- */
	if err := verifyIDs(layout, encResp.Metadata); err != nil {
		return task.wrapErr(ctx, "failed to verify IDs", err, fields)
	}
	logtrace.Info(ctx, "rq-ids have been verified", fields)
	task.streamEvent(SupernodeEventTypeRqIDsVerified, "rq-ids have been verified", "", send)

	/* 10. Simulate finalize to avoid storing artefacts if it would fail ---------- */
	if _, err := task.LumeraClient.SimulateFinalizeAction(ctx, action.ActionID, rqidResp.RQIDs); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "finalize action simulation failed", fields)
		return task.wrapErr(ctx, "finalize action simulation failed", err, fields)
	}
	logtrace.Info(ctx, "finalize action simulation passed", fields)
	// Transmit as a standard event so SDK can propagate it (dedicated type)
	task.streamEvent(SupernodeEventTypeFinalizeSimulated, "finalize action simulation passed", "", send)

	/* 11. Persist artefacts -------------------------------------------------------- */
	// Persist artefacts to the P2P network. The returned `successRate` is a
	// request-weighted percentage (0–100) computed across all underlying P2P
	// store batches for this action. Each batch contributes its success rate
	// weighted by the number of node RPCs attempted, so the aggregate reflects
	// overall network behavior rather than item counts.
	successRate, totalRequests, err := task.storeArtefacts(ctx, action.ActionID, rqidResp.RedundantMetadataFiles, encResp.SymbolsDir, fields)
	if err != nil {
		return err
	}
	// Attach the success rate to structured fields for observability. This value
	// is best-effort and non-fatal so long as it meets the configured minimum in
	// lower layers; failures below threshold would already propagate an error.
	fields["success_rate"] = successRate
	fields["requests"] = totalRequests
	logtrace.Info(ctx, "artefacts have been stored", fields)
    // Emit compact, rich metrics in the event message for external visibility.
    // ok and fail are derived counts based on the measured rate and requests.
    // TODO(move-to-request-weighted): Once aggregation switches to request-weighted,
    // these derived counts will align exactly with the per-request success rate.
    ok := int(math.Round(successRate / 100.0 * float64(totalRequests)))
    fail := totalRequests - ok
    task.streamEvent(SupernodeEventTypeArtefactsStored, fmt.Sprintf("artefacts stored | rate=%.2f%% req=%d ok=%d fail=%d", successRate, totalRequests, ok, fail), "", send)

	resp, err := task.LumeraClient.FinalizeAction(ctx, action.ActionID, rqidResp.RQIDs)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "Finalize Action Error", fields)
		return task.wrapErr(ctx, "failed to finalize action", err, fields)
	}
	txHash := resp.TxResponse.TxHash
	fields[logtrace.FieldTxHash] = txHash
	logtrace.Info(ctx, "action has been finalized", fields)
	task.streamEvent(SupernodeEventTypeActionFinalized, "action has been finalized", txHash, send)

	return nil
}
