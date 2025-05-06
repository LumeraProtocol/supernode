package cascade

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

// RegisterRequest contains parameters for upload request
type RegisterRequest struct {
	TaskID   string
	ActionID string
	Data     []byte
}

// RegisterResponse contains the result of upload
type RegisterResponse struct {
	Success bool
	Message string
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
func (task *CascadeRegistrationTask) Register(ctx context.Context, req *RegisterRequest) (*RegisterResponse, error) {
	fields := logtrace.Fields{logtrace.FieldMethod: "Register", logtrace.FieldRequest: req}

	/* 1. Fetch & validate action -------------------------------------------------- */
	action, err := task.fetchAction(ctx, req.ActionID, fields)
	if err != nil {
		return nil, err
	}

	/* 2. Verify action fee -------------------------------------------------------- */
	if err := task.verifyActionFee(ctx, action, req.Data, fields); err != nil {
		return nil, err
	}

	/* 3. Ensure this super-node is eligible -------------------------------------- */
	if err := task.ensureIsTopSupernode(ctx, uint64(action.BlockHeight), fields); err != nil {
		return nil, err
	}

	/* 4. Decode cascade metadata -------------------------------------------------- */
	cascadeMeta, err := task.decodeCascadeMetadata(ctx, action.Metadata, fields)
	if err != nil {
		return nil, err
	}

	/* 5. Verify data hash --------------------------------------------------------- */
	if err := task.verifyDataHash(ctx, req.Data, cascadeMeta.DataHash, fields); err != nil {
		return nil, err
	}

	/* 6. Encode the raw data ------------------------------------------------------ */
	encResp, err := task.encodeInput(ctx, req.Data, fields)
	if err != nil {
		return nil, err
	}

	/* 7. Signature verification + layout decode ---------------------------------- */
	layout, signature, err := task.verifySignatureAndDecodeLayout(
		ctx, cascadeMeta.Signatures, action.Creator, encResp.Metadata, fields,
	)
	if err != nil {
		return nil, err
	}

	/* 8. Generate RQ-ID files ----------------------------------------------------- */
	rqidResp, err := task.generateRQIDFiles(ctx, cascadeMeta, signature, action.Creator, encResp.Metadata, fields)
	if err != nil {
		return nil, err
	}

	/* 9. Consistency checks ------------------------------------------------------- */
	if err := verifyIDs(ctx, layout, encResp.Metadata); err != nil {
		return nil, task.wrapErr(ctx, "failed to verify IDs", err, fields)
	}

	/* 10. Persist artefacts -------------------------------------------------------- */
	if err := task.storeArtefacts(ctx, rqidResp.RedundantMetadataFiles, encResp.SymbolsDir, fields); err != nil {
		return nil, err
	}
	logtrace.Info(ctx, "artefacts have been stored", fields)

	resp, err := task.lumeraClient.ActionMsg().FinalizeCascadeAction(ctx, action.ActionID, rqidResp.RQIDs)
	if err != nil {
		logtrace.Info(ctx, "Finalize Action Error", logtrace.Fields{
			"error": err.Error(),
		})
		return nil, err
	}

	logtrace.Info(ctx, "Finalize Action Response", logtrace.Fields{
		"resp": resp.Code,
		"log":  resp.TxHash})

	// Return success when the cascade action is finalized without errors
	return &RegisterResponse{Success: true, Message: fmt.Sprintf("successfully uploaded and finalized input data with txID: %s", resp.TxHash)}, nil
}
