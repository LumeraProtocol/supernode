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
	if req != nil && req.ActionID != "" {
		ctx = logtrace.CtxWithCorrelationID(ctx, req.ActionID)
		ctx = logtrace.CtxWithOrigin(ctx, "first_pass")
		task.taskID = req.TaskID
	}

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

	if err := task.verifyActionFee(ctx, action, req.DataSize, fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: fee verified", fields)
	task.streamEvent(SupernodeEventTypeActionFeeVerified, "Action fee verified", "", send)

	fields[logtrace.FieldSupernodeState] = task.config.SupernodeAccountAddress
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return err
	}
	logtrace.Info(ctx, "register: top supernode confirmed", fields)
	task.streamEvent(SupernodeEventTypeTopSupernodeCheckPassed, "Top supernode eligibility confirmed", "", send)

	cascadeMeta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		return task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "register: metadata decoded", fields)
	task.streamEvent(SupernodeEventTypeMetadataDecoded, "Cascade metadata decoded", "", send)

	if err := cascadekit.VerifyB64DataHash(req.DataHash, cascadeMeta.DataHash); err != nil {
		return err
	}
	logtrace.Debug(ctx, "request data-hash has been matched with the action data-hash", fields)
	logtrace.Info(ctx, "register: data hash matched", fields)
	task.streamEvent(SupernodeEventTypeDataHashVerified, "Data hash verified", "", send)

	encResp, err := task.encodeInput(ctx, req.ActionID, req.FilePath, fields)
	if err != nil {
		return err
	}
	fields["symbols_dir"] = encResp.SymbolsDir
	logtrace.Info(ctx, "register: input encoded", fields)
	task.streamEvent(SupernodeEventTypeInputEncoded, "Input encoded", "", send)

	layout, signature, err := task.verifySignatureAndDecodeLayout(ctx, cascadeMeta.Signatures, action.Creator, encResp.Metadata, fields)
	if err != nil {
		return err
	}
	logtrace.Info(ctx, "register: signature verified", fields)
	task.streamEvent(SupernodeEventTypeSignatureVerified, "Signature verified", "", send)

	rqidResp, err := task.generateRQIDFiles(ctx, cascadeMeta, signature, encResp.Metadata, fields)
	if err != nil {
		return err
	}
	fields["id_files_count"] = len(rqidResp.RedundantMetadataFiles)
	logtrace.Info(ctx, "register: rqid files generated", fields)
	task.streamEvent(SupernodeEventTypeRQIDsGenerated, "RQID files generated", "", send)

	if err := cascadekit.VerifySingleBlockIDs(layout, encResp.Metadata); err != nil {
		return task.wrapErr(ctx, "failed to verify IDs", err, fields)
	}
	logtrace.Info(ctx, "register: rqids validated", fields)
	task.streamEvent(SupernodeEventTypeRqIDsVerified, "RQIDs verified", "", send)

	if _, err := task.LumeraClient.SimulateFinalizeAction(ctx, action.ActionID, rqidResp.RQIDs); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Info(ctx, "register: finalize simulation failed", fields)
		task.streamEvent(SupernodeEventTypeFinalizeSimulationFailed, "Finalize simulation failed", "", send)
		return task.wrapErr(ctx, "finalize action simulation failed", err, fields)
	}
	logtrace.Info(ctx, "register: finalize simulation passed", fields)
	task.streamEvent(SupernodeEventTypeFinalizeSimulated, "Finalize simulation passed", "", send)

	if err := task.storeArtefacts(ctx, action.ActionID, rqidResp.RedundantMetadataFiles, encResp.SymbolsDir, fields); err != nil {
		return err
	}
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
