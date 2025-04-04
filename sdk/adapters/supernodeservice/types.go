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
	Data       []byte
}

type UploadInputDataResponse struct {
	Success bool
	Message string
}

type CascadeServiceClient interface {
	UploadInputData(ctx context.Context, in *UploadInputDataRequest, opts ...grpc.CallOption) (*UploadInputDataResponse, error)
}
