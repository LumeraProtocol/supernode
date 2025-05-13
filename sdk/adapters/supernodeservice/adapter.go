package supernodeservice

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/net"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"

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
	ctx = net.AddCorrelationID(ctx)

	stream, err := a.client.Register(ctx, opts...)
	if err != nil {
		a.logger.Error(ctx, "Failed to create register stream",
			"error", err)
		return nil, err
	}

	// Open the file for reading
	file, err := os.Open(in.FilePath)
	if err != nil {
		a.logger.Error(ctx, "Failed to open file", "filePath", in.FilePath, "error", err)
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file stats for progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		a.logger.Error(ctx, "Failed to get file stats", "filePath", in.FilePath, "error", err)
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}
	totalBytes := fileInfo.Size()

	// Define chunk size
	const chunkSize = 1024 // 1 KB

	// Keep track of how much data we've processed
	bytesRead := int64(0)
	chunkIndex := 0
	buffer := make([]byte, chunkSize)

	// Read and send data in chunks
	for {
		// Read a chunk from the file
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			a.logger.Error(ctx, "Failed to read file chunk", "chunkIndex", chunkIndex, "error", err)
			return nil, fmt.Errorf("failed to read file chunk: %w", err)
		}

		// Create the chunk request with just the bytes we read
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
		progress := float64(bytesRead) / float64(totalBytes) * 100

		a.logger.Debug(ctx, "Sent data chunk", "chunkIndex", chunkIndex, "chunkSize", n, "progress", fmt.Sprintf("%.1f%%", progress))

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

	if err := stream.CloseSend(); err != nil {
		a.logger.Error(ctx, "Failed to close stream and receive response", "TaskId", in.TaskId, "ActionID", in.ActionID, "error", err)
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	// Handle streaming responses from supernode
	var finalResp *cascade.RegisterResponse
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to receive server response: %w", err)
		}

		// Log the streamed progress update
		a.logger.Info(ctx, "Supernode progress update received", "event_type", resp.EventType, "message", resp.Message, "tx_hash", resp.TxHash, "task_id", in.TaskId, "action_id", in.ActionID)

		if in.EventLogger != nil {
			in.EventLogger(ctx, toSdkEvent(resp.EventType), resp.Message, nil)
		}

		// Optionally capture the final response
		if resp.TxHash != "" {
			finalResp = resp
		}
	}

	if finalResp == nil {
		return nil, fmt.Errorf("no final response with tx_hash received")
	}

	return &CascadeSupernodeRegisterResponse{
		Success: true,
		Message: finalResp.Message,
		TxHash:  finalResp.TxHash,
	}, nil
}

// toSdkEvent converts a supernode-side enum value into an internal SDK EventType.
func toSdkEvent(e cascade.SupernodeEventType) event.EventType {
	switch e {
	case cascade.SupernodeEventType_ACTION_RETRIEVED:
		return event.TaskProgressActionRetrievedBySupernode
	case cascade.SupernodeEventType_ACTION_FEE_VERIFIED:
		return event.TaskProgressActionFeeValidated
	case cascade.SupernodeEventType_TOP_SUPERNODE_CHECK_PASSED:
		return event.TaskProgressTopSupernodeCheckValidated
	case cascade.SupernodeEventType_METADATA_DECODED:
		return event.TaskProgressCascadeMetadataDecoded
	case cascade.SupernodeEventType_DATA_HASH_VERIFIED:
		return event.TaskProgressDataHashVerified
	case cascade.SupernodeEventType_INPUT_ENCODED:
		return event.TaskProgressInputDataEncoded
	case cascade.SupernodeEventType_SIGNATURE_VERIFIED:
		return event.TaskProgressSignatureVerified
	case cascade.SupernodeEventType_RQID_GENERATED:
		return event.TaskProgressRQIDFilesGenerated
	case cascade.SupernodeEventType_RQID_VERIFIED:
		return event.TaskProgressRQIDsVerified
	case cascade.SupernodeEventType_ARTEFACTS_STORED:
		return event.TaskProgressArtefactsStored
	case cascade.SupernodeEventType_ACTION_FINALIZED:
		return event.TaskProgressActionFinalized
	default:
		return event.EventType("task.progress.unknown")
	}
}
