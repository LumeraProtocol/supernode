package cascade

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

type RecoveryReseedRequest struct {
	ActionID  string
	Signature string
}

type RecoveryReseedResult struct {
	ActionID             string
	DownloadEvents       int
	DownloadLastEvent    string
	DecodeCleanupError   string
	RQIC                 uint64
	RQMax                uint64
	DataHashVerified     bool
	IndexIDs             []string
	LayoutIDs            []string
	SymbolKeys           []string
	IndexFilesGenerated  int
	LayoutFilesGenerated int
	IDFilesGenerated     int
	SymbolsGenerated     int
}

// RecoveryReseed decodes an existing action, re-encodes the reconstructed file,
// regenerates RQ artefacts with the action's original RQ params, and stores
// them via the same store path used by register.
func (task *CascadeRegistrationTask) RecoveryReseed(ctx context.Context, req *RecoveryReseedRequest) (*RecoveryReseedResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing request")
	}
	actionID := strings.TrimSpace(req.ActionID)
	if actionID == "" {
		return nil, fmt.Errorf("missing action_id")
	}

	task.taskID = actionID
	fields := logtrace.Fields{logtrace.FieldMethod: "RecoveryReseed", logtrace.FieldActionID: actionID}

	action, err := task.fetchAction(ctx, actionID, fields)
	if err != nil {
		return nil, err
	}
	meta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		return nil, task.wrapErr(ctx, "failed to unmarshal cascade metadata", err, fields)
	}

	result := &RecoveryReseedResult{
		ActionID: actionID,
		RQIC:     meta.RqIdsIc,
		RQMax:    meta.RqIdsMax,
	}

	var decodeFilePath string
	var decodeTmpDir string

	dlErr := task.Download(ctx, &DownloadRequest{
		ActionID:               actionID,
		Signature:              req.Signature,
		BypassPrivateSignature: true,
	}, func(resp *DownloadResponse) error {
		result.DownloadEvents++
		result.DownloadLastEvent = fmt.Sprintf("%d:%s", resp.EventType, resp.Message)
		if resp.EventType == SupernodeEventTypeDecodeCompleted {
			decodeFilePath = strings.TrimSpace(resp.FilePath)
			decodeTmpDir = strings.TrimSpace(resp.DownloadedDir)
		} else if decodeFilePath == "" && strings.TrimSpace(resp.FilePath) != "" {
			decodeFilePath = strings.TrimSpace(resp.FilePath)
		}
		return nil
	})
	if dlErr != nil {
		if decodeTmpDir != "" {
			if cerr := task.CleanupDownload(ctx, decodeTmpDir); cerr != nil {
				result.DecodeCleanupError = cerr.Error()
			}
		}
		return result, dlErr
	}
	if decodeFilePath == "" {
		return result, task.wrapErr(ctx, "recovery decode did not produce a file", fmt.Errorf("missing decoded file path"), fields)
	}
	if decodeTmpDir != "" {
		defer func() {
			if cerr := task.CleanupDownload(ctx, decodeTmpDir); cerr != nil {
				result.DecodeCleanupError = cerr.Error()
				logtrace.Warn(ctx, "recovery reseed decode cleanup failed", logtrace.Fields{
					logtrace.FieldActionID: actionID,
					logtrace.FieldError:    cerr.Error(),
					"tmp_dir":              decodeTmpDir,
				})
			}
		}()
	}

	fileHash, err := utils.Blake3HashFile(decodeFilePath)
	if err != nil {
		return result, task.wrapErr(ctx, "failed to hash decoded file", err, fields)
	}
	if fileHash == nil {
		return result, task.wrapErr(ctx, "failed to hash decoded file", fmt.Errorf("file hash is nil"), fields)
	}
	if err := cascadekit.VerifyB64DataHash(fileHash, meta.DataHash); err != nil {
		return result, task.wrapErr(ctx, "decoded file hash does not match action metadata", err, fields)
	}
	result.DataHashVerified = true

	encodeResult, err := task.encodeInput(ctx, actionID, decodeFilePath, fields)
	if err != nil {
		return result, err
	}
	indexFile, layoutB64, err := task.validateIndexAndLayout(ctx, action.Creator, action.ActionID, action.AppPubkey, meta.Signatures, encodeResult.Layout)
	if err != nil {
		return result, task.wrapErr(ctx, "signature or index validation failed", err, fields)
	}
	indexIDs, layoutIDs, idFiles, err := task.generateRQIDFilesDetailed(ctx, meta, indexFile.LayoutSignature, layoutB64, fields)
	if err != nil {
		return result, err
	}
	if err := task.storeArtefacts(ctx, action.ActionID, idFiles, encodeResult.SymbolsDir, encodeResult.Layout, fields); err != nil {
		return result, err
	}

	result.IndexIDs = indexIDs
	result.LayoutIDs = layoutIDs
	result.SymbolKeys = symbolIDsFromLayout(encodeResult.Layout)
	result.IndexFilesGenerated = len(indexIDs)
	result.LayoutFilesGenerated = len(layoutIDs)
	result.IDFilesGenerated = len(idFiles)
	result.SymbolsGenerated = len(result.SymbolKeys)

	return result, nil
}

func symbolIDsFromLayout(layout codec.Layout) []string {
	seen := make(map[string]struct{}, 1024)
	for _, block := range layout.Blocks {
		for _, symbolID := range block.Symbols {
			symbolID = strings.TrimSpace(symbolID)
			if symbolID == "" {
				continue
			}
			seen[symbolID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for symbolID := range seen {
		out = append(out, symbolID)
	}
	sort.Strings(out)
	return out
}
