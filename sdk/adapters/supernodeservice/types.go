package supernodeservice

import (
	"context"

	"google.golang.org/grpc"
)

type UploadInputDataRequest struct {
	Filename   string
	ActionID   string
	DataHash   string
	SignedData string
	RqMax      int32
	FilePath   string
}

type UploadInputDataResponse struct {
	Success bool
	Message string
}

type CascadeServiceClient interface {
	UploadInputData(ctx context.Context, in *UploadInputDataRequest, opts ...grpc.CallOption) (*UploadInputDataResponse, error)
}
