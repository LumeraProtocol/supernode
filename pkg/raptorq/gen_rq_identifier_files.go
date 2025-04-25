package raptorq

import (
	"context"
	"encoding/json"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

type GenRQIdentifiersFilesRequest struct {
	BlockHash        string
	Data             []byte
	RqMax            uint32
	CreatorSNAddress string
	SignedData       string
	DoValidate       bool
	LC               lumera.Client
}

type GenRQIdentifiersFilesResponse struct {
	RQIDsIc          uint32
	RQIDs            []string
	RQIDsFiles       [][]byte
	RQIDsFile        []byte
	CreatorSignature []byte
	RQEncodeParams   EncoderParameters
}

func (s *raptorQServerClient) GenRQIdentifiersFiles(ctx context.Context, req GenRQIdentifiersFilesRequest) (
	GenRQIdentifiersFilesResponse, error) {

	// Step 1: Encode the original data to get symbol IDs
	encodeInfo, err := s.encodeInfo(ctx, req.Data, req.RqMax, req.BlockHash, req.CreatorSNAddress)
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("error encoding info: %w", err)
	}

	// Step 2: Process the symbol ID files (taking just the first one)
	var rawRQIDFile RawSymbolIDFile
	for i := range encodeInfo.SymbolIDFiles {
		rawRQIDFile = encodeInfo.SymbolIDFiles[i]
		if len(rawRQIDFile.SymbolIdentifiers) == 0 {
			return GenRQIdentifiersFilesResponse{}, errors.Errorf("empty symbol identifiers in raw file")
		}
		break // Only process the first valid file
	}

	// Step 3: Marshal and encode the raw symbol ID file
	rqIDsfile, err := json.Marshal(rawRQIDFile)
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("marshal rqID file: %w", err)
	}
	encRqIDsfile := utils.B64Encode(rqIDsfile)

	// Step 4: Validate RQIDs separately and explicitly
	if req.DoValidate {
		err := ValidateRQIDs(ctx, req.LC, req.SignedData, encRqIDsfile,
			rawRQIDFile.SymbolIdentifiers, req.CreatorSNAddress)
		if err != nil {
			return GenRQIdentifiersFilesResponse{}, errors.Errorf("error validating RQIDs: %w", err)
		}
	}

	// Step 5: Generate RQIDs using the validated data
	genRQIDsRes, err := s.generateRQIDs(ctx, generateRQIDsRequest{
		lc:             req.LC,
		rawFile:        rawRQIDFile,
		creatorAddress: req.CreatorSNAddress,
		maxFiles:       req.RqMax,
		signedData:     req.SignedData,
	})
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("error generating rqids: %w", err)
	}

	return GenRQIdentifiersFilesResponse{
		RQIDsIc:          genRQIDsRes.RQIDsIc,
		RQIDs:            genRQIDsRes.RQIDs,
		RQIDsFiles:       genRQIDsRes.RQIDsFiles,
		RQIDsFile:        genRQIDsRes.RQIDsFile,
		RQEncodeParams:   encodeInfo.EncoderParam,
		CreatorSignature: genRQIDsRes.signature,
	}, nil
}
