package cascade

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade/adaptors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/golang/protobuf/proto"
	json "github.com/json-iterator/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (task *CascadeRegistrationTask) fetchAction(ctx context.Context, actionID string, f logtrace.Fields) (*actiontypes.Action, error) {
	res, err := task.lumeraClient.GetAction(ctx, actionID)
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
	top, err := task.lumeraClient.GetTopSupernodes(ctx, blockHeight)
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

func (task *CascadeRegistrationTask) encodeInput(ctx context.Context, path string, dataSize int, f logtrace.Fields) (*adaptors.EncodeResult, error) {
	resp, err := task.rq.EncodeInput(ctx, task.ID(), path, dataSize)
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
	if err := task.lumeraClient.Verify(ctx, creator, []byte(file), sigBytes); err != nil {
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

func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, actionID string, idFiles [][]byte, symbolsDir string, f logtrace.Fields) error {
	return task.p2p.StoreArtefacts(ctx, adaptors.StoreArtefactsRequest{
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
	fee, err := task.lumeraClient.GetActionFee(ctx, strconv.Itoa(dataSizeInKBs))
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
