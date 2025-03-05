package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/supernode"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UploadInputDataRequest struct {
	ActionID string
	Filename string
	DataHash string
	RqIc     int32
	RqMax    int32
}

type UploadInputDataResponse struct {
	Success bool
	Message string
}

func (s *CascadeService) UploadInputData(ctx context.Context, req *UploadInputDataRequest) (*UploadInputDataResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldRequest: req,
	}
	action, err := s.actionClient.GetAction(ctx, action.GetActionRequest{
		ActionID: req.ActionID,
		Type:     action.CascadeActionType,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get action", fields)
		return nil, status.Errorf(codes.Internal, "failed to get action")
	}
	if action.ActionID == "" {
		logtrace.Error(ctx, "action id not found", fields)
		return nil, status.Errorf(codes.Internal, "action id not found")
	}

	latestBlock, err := s.lumeraClient.GetLatestBlock(ctx)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get latest block", fields)
		return nil, status.Errorf(codes.Internal, "failed to get latest block")
	}

	// Verify latest block is in the top 10 Supernodes
	topSNsRes, err := s.supernodeClient.GetTopSNsByBlockHeight(ctx, supernode.GetTopSupernodesForBlockRequest{
		BlockHeight: int32(latestBlock.Height),
		Limit:       10,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get top SNs", fields)
		return nil, status.Errorf(codes.Internal, "failed to get top SNs")
	}
	var found bool
	for _, sn := range topSNsRes.Supernodes {
		if sn.SupernodeAccount == s.GetSNAddress() {
			found = true
			break
		}
	}
	if !found {
		logtrace.Error(ctx, "not a valid supernode", fields)
		return nil, status.Errorf(codes.Internal, "not a valid supernode")
	}

	if req.DataHash != action.Metadata.GetCascadeMetadata().DataHash {
		logtrace.Error(ctx, "data hash doesn't match", fields)
		return nil, status.Errorf(codes.Internal, "data hash doesn't match")
	}

	// FIXME : use proper file
	s.rqClient.GenRQIdentifiersFiles(ctx, fields, nil, string(latestBlock.Hash), action.Creator, uint32(action.Metadata.GetCascadeMetadata().RqMax))

	return &UploadInputDataResponse{
		Success: true,
		Message: "successfully uploaded input data",
	}, nil
}
