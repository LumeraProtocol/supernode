package client

import (
	"context"
	"fmt"

	cascadeService "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
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

func (c *Client) UploadInputData(ctx context.Context, req UploadInputDataRequest) (UploadInputDataResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "uploading input data", fields)

	res, err := c.cascadeService.UploadInputData(ctx, &cascadeService.UploadInputDataRequest{
		Filename: req.Filename,
		ActionId: req.ActionID,
		DataHash: req.DataHash,
		RqIc:     req.RqIc,
		RqMax:    req.RqMax,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to upload input data", fields)
		return UploadInputDataResponse{}, fmt.Errorf("cascade service upload input data error: %w", err)
	}

	logtrace.Info(ctx, "successfully uploaded input data", fields)
	return UploadInputDataResponse{Success: res.Success, Message: res.Message}, nil
}
