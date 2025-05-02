package supernodeservice

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/sdk/log"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc"
)

type cascadeAdapter struct {
	client cascade.CascadeServiceClient
	logger log.Logger
}

func NewCascadeAdapter(ctx context.Context, client cascade.CascadeServiceClient, logger log.Logger) CascadeServiceClient {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Debug(ctx, "Creating cascade service adapter")

	return &cascadeAdapter{
		client: client,
		logger: logger,
	}
}

func (a *cascadeAdapter) CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error) {
	// Create the client stream
	stream, err := a.client.Register(ctx, opts...)
	if err != nil {
		a.logger.Error(ctx, "Failed to create register stream",
			"error", err)
		return nil, err
	}

	// Define chunk size
	const chunkSize = 1024 // 1 KB

	// Keep track of how much data we've processed
	bytesRead := int64(0)
	totalBytes := int64(len(in.Data))
	chunkIndex := 0

	// Read and send data in chunks
	for bytesRead < totalBytes {
		// Determine size of the next chunk
		end := bytesRead + chunkSize
		if end > totalBytes {
			end = totalBytes
		}

		// Prepare the chunk data
		chunkData := in.Data[bytesRead:end]

		// Create the chunk request
		chunk := &cascade.RegisterRequest{
			RequestType: &cascade.RegisterRequest_Chunk{
				Chunk: &cascade.DataChunk{
					Data: chunkData,
				},
			},
		}

		if err := stream.Send(chunk); err != nil {
			a.logger.Error(ctx, "Failed to send data chunk", "chunkIndex", chunkIndex, "error", err)
			return nil, fmt.Errorf("failed to send chunk: %w", err)
		}

		bytesRead += int64(len(chunkData))
		progress := float64(bytesRead) / float64(totalBytes) * 100

		a.logger.Debug(ctx, "Sent data chunk", "chunkIndex", chunkIndex, "chunkSize", len(chunkData), "progress", fmt.Sprintf("%.1f%%", progress))

		chunkIndex++
	}

	// Send metadata as the final message
	metadata := &cascade.RegisterRequest{
		RequestType: &cascade.RegisterRequest_Metadata{
			Metadata: &cascade.Metadata{
				TaskId:   in.TaskId,
				ActionId: in.ActionID,
			},
		},
	}

	if err := stream.Send(metadata); err != nil {
		a.logger.Error(ctx, "Failed to send metadata", "TaskId", in.TaskId, "ActionID", in.ActionID, "error", err)
		return nil, fmt.Errorf("failed to send metadata: %w", err)
	}

	a.logger.Debug(ctx, "Sent metadata", "TaskId", in.TaskId, "ActionID", in.ActionID)

	resp, err := stream.CloseAndRecv()
	if err != nil {
		a.logger.Error(ctx, "Failed to close stream and receive response", "TaskId", in.TaskId, "ActionID", in.ActionID, "error", err)
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	response := &CascadeSupernodeRegisterResponse{
		Success: resp.Success,
		Message: resp.Message,
	}

	a.logger.Info(ctx, "Successfully registered supernode data", "TaskId", in.TaskId, "ActionID", in.ActionID, "dataSize", totalBytes, "success", resp.Success, "message", resp.Message)

	return response, nil
}
