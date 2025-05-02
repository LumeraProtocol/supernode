package supernodeservice

import (
	"context"
	"fmt"
	"io"
	"os"

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

func (a *cascadeAdapter) RegisterCascade(ctx context.Context, in *RegisterCascadeRequest, opts ...grpc.CallOption) (*RegisterCascadeResponse, error) {
	a.logger.Debug(ctx, "RegisterCascade through adapter", "task_id", in.TaskID, "actionID", in.ActionID, "filePath", in.FilePath)

	// Open the file for reading
	file, err := os.Open(in.FilePath)
	if err != nil {
		a.logger.Error(ctx, "Failed to open file for reading",
			"error", err)
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file size for logging and progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		a.logger.Error(ctx, "Failed to get file stats",
			"error", err)
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}

	fileSize := fileInfo.Size()
	a.logger.Debug(ctx, "File opened for streaming",
		"fileSize", fileSize)

	// Create the client stream
	stream, err := a.client.Register(ctx, opts...)
	if err != nil {
		a.logger.Error(ctx, "Failed to create upload stream",
			"error", err)
		return nil, err
	}

	// Define chunk size (could be configurable)
	const chunkSize = 1024 //  1 KB
	buffer := make([]byte, chunkSize)

	// Track progress
	bytesRead := int64(0)
	chunkIndex := 0

	// Read and send file in chunks
	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			break // End of file
		}
		if err != nil {
			a.logger.Error(ctx, "Error reading file chunk",
				"chunkIndex", chunkIndex,
				"error", err)
			return nil, fmt.Errorf("error reading file: %w", err)
		}

		// Only send what was actually read
		chunk := &cascade.RegisterRequest{
			RequestType: &cascade.RegisterRequest_Chunk{
				Chunk: &cascade.DataChunk{
					Data: buffer[:n],
				},
			},
		}

		if err := stream.Send(chunk); err != nil {
			a.logger.Error(ctx, "Failed to send data chunk", "chunkIndex", chunkIndex, "error", err)
			return nil, fmt.Errorf("failed to send chunk: %w", err)
		}

		bytesRead += int64(n)
		progress := float64(bytesRead) / float64(fileSize) * 100

		a.logger.Debug(ctx, "Sent data chunk", "chunkIndex", chunkIndex, "chunkSize", n, "progress", fmt.Sprintf("%.1f%%", progress))

		chunkIndex++
	}

	// Send metadata as the final message
	metadata := &cascade.RegisterRequest{
		RequestType: &cascade.RegisterRequest_Metadata{
			Metadata: &cascade.Metadata{
				TaskId:   in.TaskID,
				ActionId: in.ActionID,
			},
		},
	}

	if err := stream.Send(metadata); err != nil {
		a.logger.Error(ctx, "Failed to send metadata", "task_id", in.TaskID, "actionID", in.ActionID, "error", err)
		return nil, fmt.Errorf("failed to send metadata: %w", err)
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		a.logger.Error(ctx, "Failed to close stream and receive response", "task_id", in.TaskID, "actionID", in.ActionID, "error", err)
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	response := &RegisterCascadeResponse{
		Success: resp.Success,
		Message: resp.Message,
	}

	a.logger.Info(ctx, "Successfully Registered with Supernode", "task_id", in.TaskID, "actionID", in.ActionID, "fileSize", fileSize,
		"success", resp.Success, "message", resp.Message)

	return response, nil
}
