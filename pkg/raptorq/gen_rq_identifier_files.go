package raptorq

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *raptorQServerClient) GenRQIdentifiersFiles(ctx context.Context, fields logtrace.Fields, data []byte, operationBlockHash string, pastelID string, rqMax uint32) (RQIDsIc uint32, RQIDs []string, RQIDsFile []byte, RQEncodeParams EncoderParameters, signature []byte, err error) {
	encodeInfo, err := s.encodeInfo(ctx, fields, data, rqMax, operationBlockHash, pastelID)
	if err != nil {
		return RQIDsIc, RQIDs, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "generate RaptorQ symbols identifiers")
	}

	var rqIDsFilesCount uint32
	for i := range encodeInfo.SymbolIDFiles {
		if len(encodeInfo.SymbolIDFiles[i].SymbolIdentifiers) == 0 {
			return RQIDsIc, RQIDs, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "empty symbol identifiers - rawFile")
		}

		RQIDsIc, RQIDs, RQIDsFile, signature, err := s.generateRQIDs(ctx, encodeInfo.SymbolIDFiles[i], pastelID, rqMax)
		if err != nil {
			return RQIDsIc, RQIDs, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "create RQIDs file")
		}
		rqIDsFilesCount++
		break
	}
	if rqIDsFilesCount != rqMax {
		return RQIDsIc, RQIDs, RQIDsFile, RQEncodeParams, signature, status.Errorf(codes.Internal, "number of RaptorQ symbol identifiers files must be %d, most probably old version of rq-services is installed", rqMax)
	}

	RQEncodeParams = encodeInfo.EncoderParam

	return RQIDsIc, RQIDs, RQIDsFile, RQEncodeParams, signature, nil
}
