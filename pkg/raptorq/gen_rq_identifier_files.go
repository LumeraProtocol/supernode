package raptorq

import (
	"context"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
)

type GenRQIdentifiersFilesRequest struct {
	TaskID           string
	BlockHash        string
	Data             []byte
	RqMax            uint32
	CreatorSNAddress string
	SignedData       string
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

	encodeInfo, err := s.encodeInfo(ctx, req.TaskID, req.Data, req.RqMax, req.BlockHash, req.CreatorSNAddress)
	if err != nil {
		return GenRQIdentifiersFilesResponse{}, errors.Errorf("error encoding info:%s", err.Error())
	}

	var genRQIDsRes generateRQIDsResponse
	for i := range encodeInfo.SymbolIDFiles {
		if len(encodeInfo.SymbolIDFiles[i].SymbolIdentifiers) == 0 {
			return GenRQIdentifiersFilesResponse{}, errors.Errorf("empty raw file")
		}

		rawRQIDFile := encodeInfo.SymbolIDFiles[i]

		genRQIDsRes, err = s.generateRQIDs(ctx, generateRQIDsRequest{
			req.LC, req.SignedData, rawRQIDFile, req.CreatorSNAddress, req.RqMax,
		})
		if err != nil {
			return GenRQIdentifiersFilesResponse{}, errors.Errorf("error generating rqids")
		}
		break
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

// // ValidateIDFiles validates received (IDs) file and its (50) IDs:
// //  1. checks signatures
// //  2. generates list of 50 IDs and compares them to received
// func (h *RegTaskHelper) ValidateIDFiles(ctx context.Context,
// 	data []byte, ic uint32, max uint32, ids []string, numSignRequired int,
// 	snAccAddresses []string,
// 	lumeraClient lumera.Client,
// 	creatorSignaure []byte,
// ) ([]byte, [][]byte, error) {

// 	dec, err := utils.B64Decode(data)
// 	if err != nil {
// 		return nil, nil, errors.Errorf("decode data: %w", err)
// 	}

// 	decData, err := utils.Decompress(dec)
// 	if err != nil {
// 		return nil, nil, errors.Errorf("decompress: %w", err)
// 	}

// 	splits := bytes.Split(decData, []byte{SeparatorByte})
// 	if len(splits) != numSignRequired+1 {
// 		return nil, nil, errors.New("invalid data")
// 	}

// 	file, err := utils.B64Decode(splits[0])
// 	if err != nil {
// 		return nil, nil, errors.Errorf("decode file: %w", err)
// 	}

// 	verifications := 0
// 	verifiedNodes := make(map[int]bool)
// 	for i := 1; i < numSignRequired+1; i++ {
// 		for j := 0; j < len(snAccAddresses); j++ {
// 			if _, ok := verifiedNodes[j]; ok {
// 				continue
// 			}

// 			err := lumeraClient.Node().Verify(snAccAddresses[j], file, creatorSignaure) // TODO : verify the signature
// 			if err != nil {
// 				return nil, nil, errors.Errorf("verify file signature %w", err)
// 			}

// 			verifiedNodes[j] = true
// 			verifications++
// 			break
// 		}
// 	}

// 	if verifications != numSignRequired {
// 		return nil, nil, errors.Errorf("file verification failed: need %d verifications, got %d", numSignRequired, verifications)
// 	}

// 	gotIDs, idFiles, err := raptorq.GetIDFiles(ctx, decData, ic, max)
// 	if err != nil {
// 		return nil, nil, errors.Errorf("get ids: %w", err)
// 	}

// 	if err := utils.EqualStrList(gotIDs, ids); err != nil {
// 		return nil, nil, errors.Errorf("IDs don't match: %w", err)
// 	}

// 	return file, idFiles, nil
// }
