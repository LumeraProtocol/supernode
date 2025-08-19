package cascade

import (
	"context"

	"bytes"

	"strconv"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/btcutil/base58"
	json "github.com/json-iterator/go"
)

const (
	SeparatorByte byte = 46 // separator in dd_and_fingerprints.signature i.e. '.'
)

// IndexFile represents the structure of the index file
type IndexFile struct {
	Version         int      `json:"version"`
	LayoutIDs       []string `json:"layout_ids"`
	LayoutSignature string   `json:"layout_signature"`
}

type GenRQIdentifiersFilesRequest struct {
	Metadata         codec.Layout
	RqMax            uint32
	CreatorSNAddress string
	Signature        string
	IC               uint32
}

type GenRQIdentifiersFilesResponse struct {
	// IDs of the Redundant Metadata Files -- len(RQIDs) == len(RedundantMetadataFiles)
	RQIDs []string
	// RedundantMetadataFiles is a list of redundant files that are generated from the Metadata file
	RedundantMetadataFiles [][]byte
}

// GenRQIdentifiersFiles generates Redundant Metadata Files and IDs
func GenRQIdentifiersFiles(ctx context.Context, req GenRQIdentifiersFilesRequest) (resp GenRQIdentifiersFilesResponse, err error) {
	metadataFile, err := json.Marshal(req.Metadata)
	if err != nil {
		return resp, errors.Errorf("marshal rqID file: %w", err)
	}
	b64EncodedMetadataFile := utils.B64Encode(metadataFile)

	// Create the RQID file by combining the encoded file with the signature
	var buffer bytes.Buffer
	buffer.Write(b64EncodedMetadataFile)
	buffer.WriteByte(SeparatorByte)
	buffer.Write([]byte(req.Signature))
	encMetadataFileWithSignature := buffer.Bytes()

	// Generate the specified number of variant IDs
	rqIdIds, rqIDsFiles, err := GetIDFiles(ctx, encMetadataFileWithSignature, req.IC, req.RqMax)
	if err != nil {
		return resp, errors.Errorf("get ID Files: %w", err)
	}

	return GenRQIdentifiersFilesResponse{
		RedundantMetadataFiles: rqIDsFiles,
		RQIDs:                  rqIdIds,
	}, nil
}

// GetIDFiles generates Redundant Files for dd_and_fingerprints files and rq_id files
// encMetadataFileWithSignature is b64 encoded layout file appended with signatures and compressed, ic is the initial counter
// and max is the number of ids to generate
func GetIDFiles(ctx context.Context, encMetadataFileWithSignature []byte, ic uint32, max uint32) (ids []string, files [][]byte, err error) {
	idFiles := make([][]byte, 0, max)
	ids = make([]string, 0, max)
	var buffer bytes.Buffer

	for i := uint32(0); i < max; i++ {
		buffer.Reset()
		counter := ic + i

		buffer.Write(encMetadataFileWithSignature)
		buffer.WriteByte(SeparatorByte)
		buffer.WriteString(strconv.Itoa(int(counter))) // Using the string representation to maintain backward compatibility

		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			return ids, idFiles, errors.Errorf("compress identifiers file: %w", err)
		}

		idFiles = append(idFiles, compressedData)

		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			return ids, idFiles, errors.Errorf("sha3-256-hash error getting an id file: %w", err)
		}

		ids = append(ids, base58.Encode(hash))
	}

	return ids, idFiles, nil
}

// GenIndexFiles generates index files and their IDs from layout files using full signatures format
func GenIndexFiles(ctx context.Context, layoutFiles [][]byte, layoutSignature string, signaturesFormat string, ic uint32, max uint32) (indexIDs []string, indexFiles [][]byte, err error) {
	// Create layout IDs from layout files
	layoutIDs := make([]string, len(layoutFiles))
	for i, layoutFile := range layoutFiles {
		hash, err := utils.Blake3Hash(layoutFile)
		if err != nil {
			return nil, nil, errors.Errorf("hash layout file: %w", err)
		}
		layoutIDs[i] = base58.Encode(hash)
	}

	// Use the full signatures format that matches what was sent during RequestAction
	// The chain expects this exact format for ID generation
	indexFileWithSignatures := []byte(signaturesFormat)

	// Generate index file IDs using full signatures format
	indexIDs, indexFiles, err = GetIDFiles(ctx, indexFileWithSignatures, ic, max)
	if err != nil {
		return nil, nil, errors.Errorf("get index ID files: %w", err)
	}

	return indexIDs, indexFiles, nil
}
