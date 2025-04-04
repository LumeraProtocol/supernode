package supernodeservice

import (
	"context"

	"action/log"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc"
)

type cascadeAdapter struct {
	client cascade.CascadeServiceClient
	logger log.Logger
}

func NewCascadeAdapter(client cascade.CascadeServiceClient, logger log.Logger) CascadeServiceClient {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	return &cascadeAdapter{
		client: client,
		logger: logger,
	}
}

func (a *cascadeAdapter) UploadInputData(ctx context.Context, in *UploadInputDataRequest, opts ...grpc.CallOption) (*UploadInputDataResponse, error) {
	a.logger.Debug(ctx, "Uploading input data through adapter",
		"filename", in.Filename,
		"actionID", in.ActionID)

	externalReq := &cascade.UploadInputDataRequest{
		Filename:   in.Filename,
		ActionId:   in.ActionID,
		DataHash:   in.DataHash,
		SignedData: in.SignedData,
		Data:       in.Data,
	}

	resp, err := a.client.UploadInputData(ctx, externalReq, opts...)
	if err != nil {
		a.logger.Error(ctx, "Failed to upload input data",
			"filename", in.Filename,
			"actionID", in.ActionID,
			"error", err)
		return nil, err
	}

	response := &UploadInputDataResponse{
		Success: resp.Success,
		Message: resp.Message,
	}

	a.logger.Info(ctx, "Successfully uploaded input data",
		"filename", in.Filename,
		"actionID", in.ActionID,
		"success", resp.Success,
		"message", resp.Message)

	return response, nil
}
