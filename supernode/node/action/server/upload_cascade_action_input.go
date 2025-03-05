package server

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/service/cascade"
)

type UploadInputDataRequest struct {
	Filename string `protobuf:"bytes,1,opt,name=filename,proto3" json:"filename,omitempty"`
	ActionId string `protobuf:"bytes,2,opt,name=action_id,json=actionId,proto3" json:"action_id,omitempty"`
	DataHash string `protobuf:"bytes,3,opt,name=data_hash,json=dataHash,proto3" json:"data_hash,omitempty"`
	RqIc     int32  `protobuf:"varint,4,opt,name=rq_ic,json=rqIc,proto3" json:"rq_ic,omitempty"`
	RqMax    int32  `protobuf:"varint,5,opt,name=rq_max,json=rqMax,proto3" json:"rq_max,omitempty"`
}

type UploadInputDataResponse struct {
	Success bool   `protobuf:"varint,1,opt,name=success,proto3" json:"success,omitempty"`
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

func (s *CascadeActionServer) UploadInputData(ctx context.Context, req *UploadInputDataRequest) (*UploadInputDataResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "uploading input data", fields)

	res, err := s.service.UploadInputData(ctx, &cascadeService.UploadInputDataRequest{
		Filename: req.Filename,
		ActionID: req.ActionId,
		DataHash: req.DataHash,
		RqIc:     req.RqIc,
		RqMax:    req.RqMax,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to upload input data", fields)
		return &UploadInputDataResponse{}, fmt.Errorf("cascade service upload input data error: %w", err)
	}

	return &UploadInputDataResponse{Success: res.Success, Message: res.Message}, nil
}
