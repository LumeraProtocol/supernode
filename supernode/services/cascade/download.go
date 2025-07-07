package cascade

import (
	"context"
	"fmt"
	"os"
	"sort"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade/adaptors"
)

const (
	requiredSymbolPercent = 9
)

type DownloadRequest struct {
	ActionID string
}

type DownloadResponse struct {
	EventType     SupernodeEventType
	Message       string
	Artefacts     []byte
	DownloadedDir string
}

func (task *CascadeRegistrationTask) Download(
	ctx context.Context,
	req *DownloadRequest,
	send func(resp *DownloadResponse) error,
) error {
	fields := logtrace.Fields{logtrace.FieldMethod: "Download", logtrace.FieldRequest: req}
	logtrace.Info(ctx, "cascade-action-download request received", fields)

	actionDetails, err := task.LumeraClient.GetAction(ctx, req.ActionID)
	if err != nil {
		fields[logtrace.FieldError] = err
		return task.wrapErr(ctx, "failed to get action", err, fields)
	}
	logtrace.Info(ctx, "action has been retrieved", fields)
	task.streamDownloadEvent(SupernodeEventTypeActionRetrieved, "action has been retrieved", nil, "", send)

	if actionDetails.GetAction().State != actiontypes.ActionStateDone {
		err = errors.New("action is not in a valid state")
		fields[logtrace.FieldError] = "action state is not done yet"
		fields[logtrace.FieldActionState] = actionDetails.GetAction().State
		return task.wrapErr(ctx, "action not found", err, fields)
	}
	logtrace.Info(ctx, "action has been validated", fields)
	task.streamDownloadEvent(SupernodeEventTypeActionFinalized, "action state has been validated", nil, "", send)

	metadata, err := task.decodeCascadeMetadata(ctx, actionDetails.GetAction().Metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "cascade metadata has been decoded", fields)
	task.streamDownloadEvent(SupernodeEventTypeMetadataDecoded, "metadata has been decoded", nil, "", send)

	file, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to download artifacts", err, fields)
	}
	logtrace.Info(ctx, "artifacts have been downloaded", fields)
	task.streamDownloadEvent(SupernodeEventTypeArtefactsDownloaded, "artifacts have been downloaded", file, tmpDir, send)

	return nil
}

func (task *CascadeRegistrationTask) downloadArtifacts(ctx context.Context, actionID string, metadata actiontypes.CascadeMetadata, fields logtrace.Fields) ([]byte, string, error) {
	logtrace.Info(ctx, "started downloading the artifacts", fields)

	var layout codec.Layout
	for _, rqID := range metadata.RqIdsIds {
		rqIDFile, err := task.P2PClient.Retrieve(ctx, rqID)
		if err != nil || len(rqIDFile) == 0 {
			continue
		}

		layout, _, _, err = parseRQMetadataFile(rqIDFile)

		if len(layout.Blocks) == 0 {
			logtrace.Info(ctx, "no symbols found in RQ metadata", fields)
			continue
		}

		//if len(layout.Blocks) < int(float64(len(metadata.RqIdsIds))*requiredSymbolPercent/100) {
		//	logtrace.Info(ctx, "not enough symbols found in RQ metadata", fields)
		//	continue
		//}

		if err == nil {
			logtrace.Info(ctx, "layout file retrieved", fields)
			break
		}
	}

	if len(layout.Blocks) == 0 {
		return nil, "", errors.New("no symbols found in RQ metadata")
	}

	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID)
}

func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
) ([]byte, string, error) {

	fields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
	}
	var allSymbols []string
	for _, block := range layout.Blocks {
		allSymbols = append(allSymbols, block.Symbols...)
	}
	sort.Strings(allSymbols)

	totalSymbols := len(allSymbols)
	requiredSymbols := (totalSymbols*requiredSymbolPercent + 99) / 100

	fields["totalSymbols"] = totalSymbols
	fields["requiredSymbols"] = requiredSymbols
	logtrace.Info(ctx, "symbols to be retrieved", fields)

	symbols, err := task.P2PClient.BatchRetrieve(ctx, allSymbols, requiredSymbols, actionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to retrieve symbols", fields)
		return nil, "", fmt.Errorf("failed to retrieve symbols: %w", err)
	}

	fields["retrievedSymbols"] = len(symbols)
	logtrace.Info(ctx, "symbols retrieved", fields)

	// 2. Decode symbols using RaptorQ
	decodeInfo, err := task.RQ.Decode(ctx, adaptors.DecodeRequest{
		ActionID: actionID,
		Symbols:  symbols,
		Layout:   layout,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to decode symbols", fields)
		return nil, "", fmt.Errorf("decode symbols using RaptorQ: %w", err)
	}

	file, err := os.ReadFile(decodeInfo.FilePath)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to read file", fields)
		return nil, "", fmt.Errorf("read decoded file: %w", err)
	}

	// 3. Validate hash (Blake3)
	fileHash, err := utils.Blake3Hash(file)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to do hash", fields)
		return nil, "", fmt.Errorf("hash file: %w", err)
	}

	err = task.verifyDataHash(ctx, fileHash, dataHash, fields)
	if err != nil {
		logtrace.Error(ctx, "failed to verify hash", fields)
		fields[logtrace.FieldError] = err.Error()
		return nil, decodeInfo.DecodeTmpDir, err
	}

	logtrace.Info(ctx, "file successfully restored and hash verified", fields)
	return file, decodeInfo.DecodeTmpDir, nil
}

func (task *CascadeRegistrationTask) streamDownloadEvent(eventType SupernodeEventType, msg string, file []byte, tmpDir string, send func(resp *DownloadResponse) error) {
	_ = send(&DownloadResponse{
		EventType:     eventType,
		Message:       msg,
		Artefacts:     file,
		DownloadedDir: tmpDir,
	})

	return
}

func (task *CascadeRegistrationTask) CleanupDownload(ctx context.Context, actionID string) error {
	if actionID == "" {
		return errors.New("actionID is empty")
	}

	// For now, we use actionID as the directory path to maintain compatibility
	if err := os.RemoveAll(actionID); err != nil {
		return errors.Errorf("failed to delete download directory: %s, :%s", actionID, err.Error())
	}

	return nil
}
