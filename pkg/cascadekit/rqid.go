package cascadekit

import (
	"context"
	"encoding/json"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// GenRQIdentifiersFilesResponse groups the generated files and their IDs.
type GenRQIdentifiersFilesResponse struct {
	// IDs of the Redundant Metadata Files -- len(RQIDs) == len(RedundantMetadataFiles)
	RQIDs []string
	// RedundantMetadataFiles is a list of redundant files generated from the Metadata file
	RedundantMetadataFiles [][]byte
}

// GenerateLayoutFiles builds redundant metadata files from layout and signature.
// The content is: base64(JSON(layout)).layout_signature
func GenerateLayoutFiles(ctx context.Context, layout codec.Layout, layoutSigB64 string, ic uint32, max uint32) (GenRQIdentifiersFilesResponse, error) {
	// Validate single-block to match package invariant
	if len(layout.Blocks) != 1 {
		return GenRQIdentifiersFilesResponse{}, errors.New("layout must contain exactly one block")
	}

	metadataFile, err := jsonMarshal(layout)
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("marshal layout: %w", err)
	}
	b64Encoded := utils.B64Encode(metadataFile)

	// Compose: base64(JSON(layout)).layout_signature
	enc := make([]byte, 0, len(b64Encoded)+1+len(layoutSigB64))
	enc = append(enc, b64Encoded...)
	enc = append(enc, SeparatorByte)
	enc = append(enc, []byte(layoutSigB64)...)

	ids, files, err := getIDFiles(enc, ic, max)
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("get ID Files: %w", err)
	}

	return GenRQIdentifiersFilesResponse{
		RedundantMetadataFiles: files,
		RQIDs:                  ids,
	}, nil
}

// GenerateIndexFiles generates index files and their IDs from the full signatures format.
func GenerateIndexFiles(ctx context.Context, signaturesFormat string, ic uint32, max uint32) (indexIDs []string, indexFiles [][]byte, err error) {
	// Use the full signatures format that matches what was sent during RequestAction
	// The chain expects this exact format for ID generation
	indexIDs, indexFiles, err = getIDFiles([]byte(signaturesFormat), ic, max)
	if err != nil {
		return nil, nil, errors.Errorf("get index ID files: %w", err)
	}
	return indexIDs, indexFiles, nil
}

// jsonMarshal marshals a value to JSON.
func jsonMarshal(v interface{}) ([]byte, error) { return json.Marshal(v) }
