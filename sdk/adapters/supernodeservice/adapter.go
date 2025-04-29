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

func (a *cascadeAdapter) UploadInputData(ctx context.Context, in *UploadInputDataRequest, opts ...grpc.CallOption) (*UploadInputDataResponse, error) {
	a.logger.Debug(ctx, "Uploading input data through adapter",
		"filename", in.Filename,
		"actionID", in.ActionID,
		"filePath", in.FilePath)

	// Open the file for reading
	file, err := os.Open(in.FilePath)
	if err != nil {
		a.logger.Error(ctx, "Failed to open file for reading",
			"filePath", in.FilePath,
			"error", err)
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file size for logging and progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		a.logger.Error(ctx, "Failed to get file stats",
			"filePath", in.FilePath,
			"error", err)
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}

	fileSize := fileInfo.Size()
	a.logger.Debug(ctx, "File opened for streaming",
		"filePath", in.FilePath,
		"fileSize", fileSize)

	// Create the client stream
	stream, err := a.client.UploadInputData(ctx, opts...)
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
		chunk := &cascade.UploadInputDataRequest{
			RequestType: &cascade.UploadInputDataRequest_Chunk{
				Chunk: &cascade.DataChunk{
					Data: buffer[:n],
				},
			},
		}

		if err := stream.Send(chunk); err != nil {
			a.logger.Error(ctx, "Failed to send data chunk",
				"chunkIndex", chunkIndex,
				"error", err)
			return nil, fmt.Errorf("failed to send chunk: %w", err)
		}

		bytesRead += int64(n)
		progress := float64(bytesRead) / float64(fileSize) * 100

		a.logger.Debug(ctx, "Sent data chunk",
			"chunkIndex", chunkIndex,
			"chunkSize", n,
			"progress", fmt.Sprintf("%.1f%%", progress))

		chunkIndex++
	}

	// Send metadata as the final message
	metadata := &cascade.UploadInputDataRequest{
		RequestType: &cascade.UploadInputDataRequest_Metadata{
			Metadata: &cascade.Metadata{
				Filename:   in.Filename,
				ActionId:   in.ActionID,
				DataHash:   in.DataHash,
				RqMax:      in.RqMax,
				SignedData: in.SignedData,
			},
		},
	}

	if err := stream.Send(metadata); err != nil {
		a.logger.Error(ctx, "Failed to send metadata",
			"filename", in.Filename,
			"actionID", in.ActionID,
			"error", err)
		return nil, fmt.Errorf("failed to send metadata: %w", err)
	}

	a.logger.Debug(ctx, "Sent metadata",
		"filename", in.Filename,
		"actionID", in.ActionID)

	resp, err := stream.CloseAndRecv()
	if err != nil {
		a.logger.Error(ctx, "Failed to close stream and receive response",
			"filename", in.Filename,
			"actionID", in.ActionID,
			"error", err)
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	response := &UploadInputDataResponse{
		Success: resp.Success,
		Message: resp.Message,
	}

	a.logger.Info(ctx, "Successfully uploaded input data",
		"filename", in.Filename,
		"actionID", in.ActionID,
		"fileSize", fileSize,
		"success", resp.Success,
		"message", resp.Message)

	return response, nil
}
