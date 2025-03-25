package raptorq

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *raptorQServerClient) GenRQIdentifiersFiles(ctx context.Context,
	taskID string,
	data []byte,
	operationBlockHash string,
	creator string,
	rqMax uint32) (RQIDsIc uint32, RQIDs []string,
	RQIDsFiles [][]byte,
	RQIDsFile []byte,
	RQEncodeParams EncoderParameters,
	signature []byte,
	err error) {

	encodeInfo, err := s.encodeInfo(ctx, taskID, data, rqMax, operationBlockHash, creator)
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFiles, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "generate RaptorQ symbols identifiers")
	}

	var rqIDsFilesCount uint32
	for i := range encodeInfo.SymbolIDFiles {
		if len(encodeInfo.SymbolIDFiles[i].SymbolIdentifiers) == 0 {
			return RQIDsIc, RQIDs, RQIDsFiles, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "empty symbol identifiers - rawFile")
		}

		RQIDsIc, RQIDs, RQIDsFile, RQIDsFiles, signature, err := s.generateRQIDs(ctx, encodeInfo.SymbolIDFiles[i], creator, rqMax)
		if err != nil {
			return RQIDsIc, RQIDs, RQIDsFiles, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "create RQIDs file")
		}
		rqIDsFilesCount++
		break
	}
	if rqIDsFilesCount != rqMax {
		return RQIDsIc, RQIDs, RQIDsFiles, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "number of RaptorQ symbol identifiers files must be %d, most probably old version of rq-services is installed", rqMax)
	}

	RQEncodeParams = encodeInfo.EncoderParam

	return RQIDsIc, RQIDs, RQIDsFiles, RQIDsFile, RQEncodeParams, signature, nil
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
