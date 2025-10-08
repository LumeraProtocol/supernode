package cascade

import (
	"context"
	"encoding/base64"
	"strconv"

	"cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"

	"github.com/LumeraProtocol/supernode/v2/supernode/cascade/adaptors"

	sdk "github.com/cosmos/cosmos-sdk/types"
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
		addresses := make([]string, len(top.Supernodes))
		for i, sn := range top.Supernodes {
			addresses[i] = sn.SupernodeAccount
		}
		logtrace.Debug(ctx, "Supernode not in top list", logtrace.Fields{"currentAddress": task.config.SupernodeAccountAddress, "topSupernodes": addresses})
		return task.wrapErr(ctx, "current supernode does not exist in the top SNs list", errors.Errorf("current address: %s, top supernodes: %v", task.config.SupernodeAccountAddress, addresses), f)
	}
	return nil
}

func (task *CascadeRegistrationTask) encodeInput(ctx context.Context, actionID string, path string, f logtrace.Fields) (*adaptors.EncodeResult, error) {
	resp, err := task.RQ.EncodeInput(ctx, actionID, path)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to encode data", err, f)
	}
	return &resp, nil
}

func (task *CascadeRegistrationTask) verifySignatureAndDecodeLayout(ctx context.Context, encoded string, creator string, encodedMeta codec.Layout, f logtrace.Fields) (codec.Layout, string, error) {
	indexFileB64, creatorSig, err := cascadekit.ExtractIndexAndCreatorSig(encoded)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to extract index file and creator signature", err, f)
	}
	creatorSigBytes, err := base64.StdEncoding.DecodeString(creatorSig)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode creator signature from base64", err, f)
	}
	if err := task.LumeraClient.Verify(ctx, creator, []byte(indexFileB64), creatorSigBytes); err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to verify creator signature", err, f)
	}
	logtrace.Debug(ctx, "creator signature successfully verified", f)
	indexFile, err := cascadekit.DecodeIndexB64(indexFileB64)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode index file", err, f)
	}
	layoutSigBytes, err := base64.StdEncoding.DecodeString(indexFile.LayoutSignature)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to decode layout signature from base64", err, f)
	}
	layoutB64, err := cascadekit.LayoutB64(encodedMeta)
	if err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to build layout base64", err, f)
	}
	if err := task.LumeraClient.Verify(ctx, creator, layoutB64, layoutSigBytes); err != nil {
		return codec.Layout{}, "", task.wrapErr(ctx, "failed to verify layout signature", err, f)
	}
	logtrace.Debug(ctx, "layout signature successfully verified", f)
	return encodedMeta, indexFile.LayoutSignature, nil
}

func (task *CascadeRegistrationTask) generateRQIDFiles(ctx context.Context, meta actiontypes.CascadeMetadata, sig string, encodedMeta codec.Layout, f logtrace.Fields) (cascadekit.GenRQIdentifiersFilesResponse, error) {
	layoutRes, err := cascadekit.GenerateLayoutFiles(ctx, encodedMeta, sig, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	if err != nil {
		return cascadekit.GenRQIdentifiersFilesResponse{}, task.wrapErr(ctx, "failed to generate layout files", err, f)
	}
	indexIDs, indexFiles, err := cascadekit.GenerateIndexFiles(ctx, meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	if err != nil {
		return cascadekit.GenRQIdentifiersFilesResponse{}, task.wrapErr(ctx, "failed to generate index files", err, f)
	}
	allFiles := append(layoutRes.RedundantMetadataFiles, indexFiles...)
	return cascadekit.GenRQIdentifiersFilesResponse{RQIDs: indexIDs, RedundantMetadataFiles: allFiles}, nil
}

func (task *CascadeRegistrationTask) storeArtefacts(ctx context.Context, actionID string, idFiles [][]byte, symbolsDir string, f logtrace.Fields) error {
	if f == nil {
		f = logtrace.Fields{}
	}
	lf := logtrace.Fields{logtrace.FieldActionID: actionID, logtrace.FieldTaskID: task.taskID, "id_files_count": len(idFiles), "symbols_dir": symbolsDir}
	for k, v := range f {
		lf[k] = v
	}
	ctx = logtrace.CtxWithOrigin(ctx, "first_pass")
	logtrace.Info(ctx, "store: first-pass begin", lf)
	if err := task.P2P.StoreArtefacts(ctx, adaptors.StoreArtefactsRequest{IDFiles: idFiles, SymbolsDir: symbolsDir, TaskID: task.taskID, ActionID: actionID}, f); err != nil {
		return task.wrapErr(ctx, "failed to store artefacts", err, lf)
	}
	return nil
}

func (task *CascadeRegistrationTask) wrapErr(ctx context.Context, msg string, err error, f logtrace.Fields) error {
	if err != nil {
		f[logtrace.FieldError] = err.Error()
	}
	logtrace.Error(ctx, msg, f)
	if err != nil {
		return status.Errorf(codes.Internal, "%s: %v", msg, err)
	}
	return status.Errorf(codes.Internal, "%s", msg)
}

func (task *CascadeRegistrationTask) emitArtefactsStored(ctx context.Context, fields logtrace.Fields, _ codec.Layout, send func(resp *RegisterResponse) error) {
	if fields == nil {
		fields = logtrace.Fields{}
	}
	msg := "Artefacts stored"
	logtrace.Debug(ctx, "artefacts have been stored", fields)
	task.streamEvent(SupernodeEventTypeArtefactsStored, msg, "", send)
}

func (task *CascadeRegistrationTask) verifyActionFee(ctx context.Context, action *actiontypes.Action, dataSize int, fields logtrace.Fields) error {
	dataSizeInKBs := dataSize / 1024
	fee, err := task.LumeraClient.GetActionFee(ctx, strconv.Itoa(dataSizeInKBs))
	if err != nil {
		return task.wrapErr(ctx, "failed to get action fee", err, fields)
	}
	amount, err := strconv.ParseInt(fee.Amount, 10, 64)
	if err != nil {
		return task.wrapErr(ctx, "failed to parse fee amount", err, fields)
	}
	requiredFee := sdk.NewCoin("ulume", math.NewInt(amount))
	logtrace.Debug(ctx, "calculated required fee", logtrace.Fields{"fee": requiredFee.String(), "dataBytes": dataSize})
	if action.Price == nil || action.Price.String() != requiredFee.String() {
		got := "<nil>"
		if action.Price != nil {
			got = action.Price.String()
		}
		return task.wrapErr(ctx, "insufficient fee", errors.Errorf("expected at least %s, got %s", requiredFee.String(), got), fields)
	}
	return nil
}
