package cascade

import (
	"bytes"
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

func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, actionID string, idFiles [][]byte, symbolsDir string, f logtrace.Fields) error {
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
