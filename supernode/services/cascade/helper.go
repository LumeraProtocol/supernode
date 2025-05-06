package cascade

import (
	"context"
	"encoding/base64"
	"fmt"

	"strings"

	"cosmossdk.io/math"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/golang/protobuf/proto"
	json "github.com/json-iterator/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/types"
)

func (task *CascadeRegistrationTask) fetchAction(ctx context.Context, actionID string, f logtrace.Fields) (*actiontypes.Action, error) {
	res, err := task.lumeraClient.Action().GetAction(ctx, actionID)
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
	top, err := task.lumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, blockHeight)
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

func (task *CascadeRegistrationTask) verifyDataHash(ctx context.Context, data []byte, expected string, f logtrace.Fields) error {
	dh, _ := utils.Blake3Hash(data)
	b64 := utils.B64Encode(dh)
	if string(b64) != expected {
		return task.wrapErr(ctx, "data hash doesn't match", errors.New(""), f)
	}
	logtrace.Info(ctx, "request data-hash has been matched with the action data-hash", f)

	return nil
}

func (task *CascadeRegistrationTask) encodeInput(ctx context.Context, data []byte, f logtrace.Fields) (*codec.EncodeResponse, error) {
	resp, err := task.codec.Encode(ctx, codec.EncodeRequest{Data: data, TaskID: task.ID()})
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to encode data", err, f)
	}
	return &resp, nil
}

func (task *CascadeRegistrationTask) verifySignatureAndDecodeLayout(ctx context.Context, encoded string, creator string,
	encodedMeta codec.Layout, f logtrace.Fields) (codec.Layout, string, error) {

	file, sig, err := extractSignatureAndFirstPart(encoded)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to extract signature and first part", err, f)
	}
	logtrace.Info(ctx, "signature and first part have been extracted", f)

	// Decode the base64-encoded signature
	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode signature from base64", err, f)
	}

	// Log the verification attempt for the node creator
	logtrace.Info(ctx, "verifying signature from node creator", logtrace.Fields{
		"creator": creator,
		"taskID":  task.ID(),
	})

	// Pass the decoded signature bytes for verification
	if err := task.lumeraClient.Auth().Verify(ctx, creator, []byte(file), sigBytes); err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to verify node creator signature", err, f)
	}

	logtrace.Info(ctx, "node creator signature successfully verified", f)

	layout, err := decodeMetadataFile(file)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode metadata file", err, f)
	}

	return layout, sig, nil
}

func (task *CascadeRegistrationTask) generateRQIDFiles(ctx context.Context, meta actiontypes.CascadeMetadata,
	sig, creator string, encodedMeta codec.Layout, f logtrace.Fields) (GenRQIdentifiersFilesResponse, error) {
	res, err := GenRQIdentifiersFiles(ctx, GenRQIdentifiersFilesRequest{
		Metadata:         encodedMeta,
		CreatorSNAddress: creator,
		RqMax:            uint32(meta.RqIdsMax),
		Signature:        sig,
		IC:               uint32(meta.RqIdsIc),
	})
	if err != nil {
		return GenRQIdentifiersFilesResponse{},
			task.wrapErr(ctx, "failed to generate RQID Files", err, f)
	}
	logtrace.Info(ctx, "rq symbols, rq-ids and rqid-files have been generated", f)
	return res, nil
}

func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, idFiles [][]byte, symbolsDir string, f logtrace.Fields) error {
	logtrace.Info(ctx, "About to store ID files", logtrace.Fields{"taskID": task.ID(), "fileCount": len(idFiles)})

	if err := task.storeIDFiles(ctx, idFiles); err != nil {
		return task.wrapErr(ctx, "failed to store ID files", err, f)
	}
	logtrace.Info(ctx, "id files have been stored", f)

	if err := task.storeRaptorQSymbols(ctx, symbolsDir); err != nil {
		return task.wrapErr(ctx, "error storing raptor-q symbols", err, f)
	}
	logtrace.Info(ctx, "raptor-q symbols have been stored", f)

	return nil
}

func (task *CascadeRegistrationTask) wrapErr(ctx context.Context, msg string, err error, f logtrace.Fields) error {
	if err != nil {
		f[logtrace.FieldError] = err.Error()
	}
	logtrace.Error(ctx, msg, f)

	return status.Errorf(codes.Internal, "%s", msg)
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

func verifyIDs(ctx context.Context, ticketMetadata, metadata codec.Layout) error {
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

// storeIDFiles stores ID files to P2P
func (task *CascadeRegistrationTask) storeIDFiles(ctx context.Context, metadataFiles [][]byte) error {
	ctx = context.WithValue(ctx, log.TaskIDKey, task.ID())
	task.storage.TaskID = task.ID()

	// Log basic info before storing
	logtrace.Info(ctx, "Storing ID files", logtrace.Fields{
		"taskID": task.ID(),
	})

	// Check if files exist
	if len(metadataFiles) == 0 {
		logtrace.Error(ctx, "No ID files to store", nil)
		return errors.New("no ID files to store")
	}

	// Store files with better error handling
	if err := task.storage.StoreBatch(ctx, metadataFiles, common.P2PDataCascadeMetadata); err != nil {
		logtrace.Error(ctx, "Store operation failed", logtrace.Fields{
			"error":     err.Error(),
			"fileCount": len(metadataFiles),
		})
		return errors.Errorf("store ID files into kademlia: %w", err)
	}

	logtrace.Info(ctx, "ID files stored successfully", nil)
	return nil
}

// storeRaptorQSymbols stores RaptorQ symbols to P2P
func (task *CascadeRegistrationTask) storeRaptorQSymbols(ctx context.Context, symbolsDir string) error {
	// Add improved logging
	logtrace.Info(ctx, "Storing RaptorQ symbols", logtrace.Fields{
		"taskID": task.ID(),
	})

	err := task.storage.StoreRaptorQSymbolsIntoP2P(ctx, task.ID(), symbolsDir)
	if err != nil {
		logtrace.Error(ctx, "Failed to store RaptorQ symbols", logtrace.Fields{
			"taskID": task.ID(),
			"error":  err.Error(),
		})
	}
	return err
}

// verifyActionFee checks if the action fee is sufficient for the given data size
// It fetches action parameters, calculates the required fee, and compares it with the action price
func (task *CascadeRegistrationTask) verifyActionFee(ctx context.Context, action *actiontypes.Action, data []byte, fields logtrace.Fields) error {
	// Fetch action parameters
	params, err := task.lumeraClient.Action().GetParams(ctx)
	if err != nil {
		return task.wrapErr(ctx, "failed to get action parameters", err, fields)
	}

	// Get base fee and fee per byte
	baseFee := params.Params.BaseActionFee
	feePerByte := params.Params.FeePerByte.Amount

	// Calculate per-byte fee based on data size
	dataBytes := len(data)
	perByteFee := sdk.NewCoin(baseFee.Denom, feePerByte.Mul(math.NewInt(int64(dataBytes))))

	// Calculate total fee
	requiredFee := baseFee.Add(perByteFee)

	// Log the calculated fee
	logtrace.Info(ctx, "calculated required fee", logtrace.Fields{
		"fee":       requiredFee.String(),
		"dataBytes": dataBytes,
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
