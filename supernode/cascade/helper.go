package cascade

import (
	"context"
	"strconv"
	"strings"

	"cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"

	"github.com/LumeraProtocol/supernode/v2/supernode/adaptors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (task *CascadeRegistrationTask) fetchAction(ctx context.Context, actionID string, f logtrace.Fields) (*actiontypes.Action, error) {
	if f == nil {
		f = logtrace.Fields{}
	}
	f[logtrace.FieldActionID] = actionID
	logtrace.Info(ctx, "register: fetch action start", f)
	res, err := task.LumeraClient.GetAction(ctx, actionID)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to get action", err, f)
	}
	if res.GetAction().ActionID == "" {
		return nil, task.wrapErr(ctx, "action not found", errors.New(""), f)
	}
	logtrace.Info(ctx, "register: fetch action ok", f)
	return res.GetAction(), nil
}

func (task *CascadeRegistrationTask) ensureIsTopSupernode(ctx context.Context, blockHeight uint64, f logtrace.Fields) error {
	if f == nil {
		f = logtrace.Fields{}
	}
	f[logtrace.FieldBlockHeight] = blockHeight
	logtrace.Info(ctx, "register: top-supernodes fetch start", f)
	top, err := task.LumeraClient.GetTopSupernodes(ctx, blockHeight)
	if err != nil {
		return task.wrapErr(ctx, "failed to get top SNs", err, f)
	}
	logtrace.Info(ctx, "register: top-supernodes fetch ok", f)
	if !supernode.Exists(top.Supernodes, task.SupernodeAccountAddress) {
		addresses := make([]string, len(top.Supernodes))
		for i, sn := range top.Supernodes {
			addresses[i] = sn.SupernodeAccount
		}
		logtrace.Debug(ctx, "Supernode not in top list", logtrace.Fields{"currentAddress": task.SupernodeAccountAddress, "topSupernodes": addresses})
		return task.wrapErr(ctx, "current supernode does not exist in the top SNs list", errors.Errorf("current address: %s, top supernodes: %v", task.SupernodeAccountAddress, addresses), f)
	}
	logtrace.Info(ctx, "register: top-supernode verified", f)
	return nil
}

func (task *CascadeRegistrationTask) encodeInput(ctx context.Context, actionID string, filePath string, f logtrace.Fields) (*adaptors.EncodeResult, error) {
	if f == nil {
		f = logtrace.Fields{}
	}
	f[logtrace.FieldActionID] = actionID
	f["file_path"] = filePath
	logtrace.Info(ctx, "register: encode input start", f)
	res, err := task.RQ.EncodeInput(ctx, actionID, filePath)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to encode data", err, f)
	}
	// Enrich fields with result for subsequent logs
	f["symbols_dir"] = res.SymbolsDir
	logtrace.Info(ctx, "register: encode input ok", f)
	return &res, nil
}

// ValidateIndexAndLayout verifies:
// - creator signature over the index payload (index_b64)
// - layout signature over base64(JSON(layout))
// Returns the decoded index and layoutB64. No logging here; callers handle it.
func (task *CascadeRegistrationTask) validateIndexAndLayout(ctx context.Context, creator string, indexSignatureFormat string, layout codec.Layout) (cascadekit.IndexFile, []byte, error) {
	// Extract and verify creator signature on index
	indexB64, creatorSigB64, err := cascadekit.ExtractIndexAndCreatorSig(indexSignatureFormat)
	if err != nil {
		return cascadekit.IndexFile{}, nil, err
	}
	if err := cascadekit.VerifyIndex(indexB64, creatorSigB64, creator, func(data, sig []byte) error {
		return task.LumeraClient.Verify(ctx, creator, data, sig)
	}); err != nil {
		return cascadekit.IndexFile{}, nil, err
	}
	// Decode index
	indexFile, err := cascadekit.DecodeIndexB64(indexB64)
	if err != nil {
		return cascadekit.IndexFile{}, nil, err
	}
	// Build layoutB64 and verify single-block + signature
	layoutB64, err := cascadekit.LayoutB64(layout)
	if err != nil {
		return cascadekit.IndexFile{}, nil, err
	}
	// Enforce single-block layout for Cascade
	if len(layout.Blocks) != 1 {
		return cascadekit.IndexFile{}, nil, errors.New("layout must contain exactly one block")
	}
	if err := cascadekit.VerifyLayout(layoutB64, indexFile.LayoutSignature, creator, func(data, sig []byte) error {
		return task.LumeraClient.Verify(ctx, creator, data, sig)
	}); err != nil {
		return cascadekit.IndexFile{}, nil, err
	}
	return indexFile, layoutB64, nil
}

func (task *CascadeRegistrationTask) generateRQIDFiles(ctx context.Context, meta actiontypes.CascadeMetadata, layoutSigB64 string, layoutB64 []byte, f logtrace.Fields) ([]string, [][]byte, error) {
	if f == nil {
		f = logtrace.Fields{}
	}
	f["rq_ic"] = uint32(meta.RqIdsIc)
	f["rq_max"] = uint32(meta.RqIdsMax)
	logtrace.Info(ctx, "register: rqid files generation start", f)

	layoutIDs, layoutFiles, err := cascadekit.GenerateLayoutFilesFromB64(layoutB64, layoutSigB64, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	if err != nil {
		return nil, nil, task.wrapErr(ctx, "failed to generate layout files", err, f)
	}
	logtrace.Info(ctx, "register: layout files generated", logtrace.Fields{"count": len(layoutFiles), "layout_ids": len(layoutIDs)})
	indexIDs, indexFiles, err := cascadekit.GenerateIndexFiles(meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	if err != nil {
		return nil, nil, task.wrapErr(ctx, "failed to generate index files", err, f)
	}
	allFiles := append(layoutFiles, indexFiles...)
	logtrace.Info(ctx, "register: index files generated", logtrace.Fields{"count": len(indexFiles), "rqids": len(indexIDs)})
	logtrace.Info(ctx, "register: rqid files generation ok", logtrace.Fields{"total_files": len(allFiles)})
	return indexIDs, allFiles, nil
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
	logtrace.Info(ctx, "store: first-pass ok", lf)
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
	logtrace.Info(ctx, "register: artefacts stored", fields)
	task.streamEvent(SupernodeEventTypeArtefactsStored, msg, "", send)
}

func (task *CascadeRegistrationTask) verifyActionFee(ctx context.Context, action *actiontypes.Action, dataSize int, fields logtrace.Fields) error {
	if fields == nil {
		fields = logtrace.Fields{}
	}
	fields["data_bytes"] = dataSize
	logtrace.Info(ctx, "register: verify action fee start", fields)
	// Round up to the nearest KB to avoid underestimating required fee
	dataSizeInKBs := (dataSize + 1023) / 1024
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
	// Accept paying more than the minimum required fee. Only enforce denom match and Amount >= required.
	if strings.TrimSpace(action.Price) == "" {
		return task.wrapErr(ctx, "insufficient fee", errors.Errorf("expected at least %s, got empty price", requiredFee.String()), fields)
	}
	providedFee, err := sdk.ParseCoinNormalized(action.Price)
	if err != nil {
		return task.wrapErr(ctx, "invalid fee format", errors.Errorf("price parse error: %v", err), fields)
	}
	if providedFee.Denom != requiredFee.Denom {
		return task.wrapErr(ctx, "invalid fee denom", errors.Errorf("expected denom %s, got %s", requiredFee.Denom, providedFee.Denom), fields)
	}
	if providedFee.Amount.LT(requiredFee.Amount) {
		return task.wrapErr(ctx, "insufficient fee", errors.Errorf("expected at least %s, got %s", requiredFee.String(), providedFee.String()), fields)
	}
	logtrace.Info(ctx, "register: verify action fee ok", logtrace.Fields{"required_fee": requiredFee.String(), "provided_fee": providedFee.String()})
	return nil
}
