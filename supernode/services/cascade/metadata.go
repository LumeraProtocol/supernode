package cascade

import (
	"context"

	"bytes"

	"strconv"

	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/cosmos/btcutil/base58"
)

const (
	SeparatorByte byte = 46 // separator in dd_and_fingerprints.signature i.e. '.'
)

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

func GenRQIdentifiersFiles(ctx context.Context, req GenRQIdentifiersFilesRequest) (resp GenRQIdentifiersFilesResponse, err error) {
	// Since verifySignatures confirms that the signature is valid, we can use the signature from ticket directly

	encMetadataFileWithSignature := []byte(req.Signature)
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
