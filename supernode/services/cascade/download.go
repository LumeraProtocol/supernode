package cascade

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/crypto"
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
	FilePath      string
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
	task.streamDownloadEvent(SupernodeEventTypeActionRetrieved, "action has been retrieved", "", "", send)

	if actionDetails.GetAction().State != actiontypes.ActionStateDone {
		err = errors.New("action is not in a valid state")
		fields[logtrace.FieldError] = "action state is not done yet"
		fields[logtrace.FieldActionState] = actionDetails.GetAction().State
		return task.wrapErr(ctx, "action not found", err, fields)
	}
	logtrace.Info(ctx, "action has been validated", fields)
	task.streamDownloadEvent(SupernodeEventTypeActionFinalized, "action state has been validated", "", "", send)

	metadata, err := task.decodeCascadeMetadata(ctx, actionDetails.GetAction().Metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "error decoding cascade metadata", err, fields)
	}
	logtrace.Info(ctx, "cascade metadata has been decoded", fields)
	task.streamDownloadEvent(SupernodeEventTypeMetadataDecoded, "metadata has been decoded", "", "", send)

	filePath, tmpDir, err := task.downloadArtifacts(ctx, actionDetails.GetAction().ActionID, metadata, fields)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return task.wrapErr(ctx, "failed to download artifacts", err, fields)
	}
	logtrace.Info(ctx, "artifacts have been downloaded", fields)
	task.streamDownloadEvent(SupernodeEventTypeArtefactsDownloaded, "artifacts have been downloaded", filePath, tmpDir, send)

	return nil
}

func (task *CascadeRegistrationTask) downloadArtifacts(ctx context.Context, actionID string, metadata actiontypes.CascadeMetadata, fields logtrace.Fields) (string, string, error) {
	logtrace.Info(ctx, "started downloading the artifacts", fields)

	var layout codec.Layout

	for _, indexID := range metadata.RqIdsIds {
		indexFile, err := task.P2PClient.Retrieve(ctx, indexID)
		if err != nil || len(indexFile) == 0 {
			continue
		}

		// Parse index file to get layout IDs
		indexData, err := task.parseIndexFile(indexFile)
		if err != nil {
			logtrace.Info(ctx, "failed to parse index file", fields)
			continue
		}

		// Try to retrieve layout files using layout IDs from index file
		layout, err = task.retrieveLayoutFromIndex(ctx, indexData, fields)
		if err != nil {
			logtrace.Info(ctx, "failed to retrieve layout from index", fields)
			continue
		}

		if len(layout.Blocks) > 0 {
			logtrace.Info(ctx, "layout file retrieved via index", fields)
			break
		}
	}

	if len(layout.Blocks) == 0 {
		return "", "", errors.New("no symbols found in RQ metadata")
	}

	return task.restoreFileFromLayout(ctx, layout, metadata.DataHash, actionID)
}

func (task *CascadeRegistrationTask) restoreFileFromLayout(
	ctx context.Context,
	layout codec.Layout,
	dataHash string,
	actionID string,
) (string, string, error) {

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
		return "", "", fmt.Errorf("failed to retrieve symbols: %w", err)
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
		return "", "", fmt.Errorf("decode symbols using RaptorQ: %w", err)
	}

	fileHash, err := crypto.HashFileIncrementally(decodeInfo.FilePath, 0)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", fmt.Errorf("hash file: %w", err)
	}
	if fileHash == nil {
		fields[logtrace.FieldError] = "file hash is nil"
		logtrace.Error(ctx, "failed to hash file", fields)
		return "", "", errors.New("file hash is nil")
	}

	err = task.verifyDataHash(ctx, fileHash, dataHash, fields)
	if err != nil {
		logtrace.Error(ctx, "failed to verify hash", fields)
		fields[logtrace.FieldError] = err.Error()
		return "", decodeInfo.DecodeTmpDir, err
	}
	logtrace.Info(ctx, "file successfully restored and hash verified", fields)

	return decodeInfo.FilePath, decodeInfo.DecodeTmpDir, nil
}

func (task *CascadeRegistrationTask) streamDownloadEvent(eventType SupernodeEventType, msg string, filePath string, tmpDir string, send func(resp *DownloadResponse) error) {
	_ = send(&DownloadResponse{
		EventType:     eventType,
		Message:       msg,
		FilePath:      filePath,
		DownloadedDir: tmpDir,
	})
}

// parseIndexFile parses compressed index file to extract IndexFile structure
func (task *CascadeRegistrationTask) parseIndexFile(data []byte) (IndexFile, error) {
	decompressed, err := utils.ZstdDecompress(data)
	if err != nil {
		return IndexFile{}, errors.Errorf("decompress index file: %w", err)
	}

	// Parse decompressed data: base64IndexFile.signature.counter
	parts := bytes.Split(decompressed, []byte{SeparatorByte})
	if len(parts) < 2 {
		return IndexFile{}, errors.New("invalid index file format")
	}

	// Decode the base64 index file
	return decodeIndexFile(string(parts[0]))
}

// retrieveLayoutFromIndex retrieves layout file using layout IDs from index file
func (task *CascadeRegistrationTask) retrieveLayoutFromIndex(ctx context.Context, indexData IndexFile, fields logtrace.Fields) (codec.Layout, error) {
	// Try to retrieve layout files using layout IDs from index file
	for _, layoutID := range indexData.LayoutIDs {
		layoutFile, err := task.P2PClient.Retrieve(ctx, layoutID)
		if err != nil || len(layoutFile) == 0 {
			continue
		}

		layout, _, _, err := parseRQMetadataFile(layoutFile)
		if err != nil {
			continue
		}

		if len(layout.Blocks) > 0 {
			return layout, nil
		}
	}

	return codec.Layout{}, errors.New("no valid layout found in index")
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
