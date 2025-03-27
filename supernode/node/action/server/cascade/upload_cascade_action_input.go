package cascade

import (
	"context"
	"fmt"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/services/cascade"
)

func (s *CascadeActionServer) UploadInputData(ctx context.Context, req *pb.UploadInputDataRequest) (*pb.UploadInputDataResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldModule:  "CascadeActionServer",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "request to upload cascade input data received", fields)

	task, err := s.TaskFromMD(ctx)
	if err != nil {
		return nil, err
	}

	res, err := task.UploadInputData(ctx, &cascadeService.UploadInputDataRequest{
		Filename:   req.Filename,
		ActionID:   req.ActionId,
		DataHash:   req.DataHash,
		RqMax:      req.RqMax,
		SignedData: req.SignedData,
		Data:       req.Data,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to upload input data", fields)
		return &pb.UploadInputDataResponse{}, fmt.Errorf("cascade services upload input data error: %w", err)
	}

	return &pb.UploadInputDataResponse{Success: res.Success, Message: res.Message}, nil
}
