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
			in.EventLogger(ctx, toSdkEvent(resp.EventType), resp.Message, event.EventData{
				event.KeyEventType: resp.EventType,
				event.KeyMessage:   resp.Message,
				event.KeyTxHash:    resp.TxHash,
				event.KeyTaskID:    in.TaskId,
				event.KeyActionID:  in.ActionID,
			})
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

// CascadeSupernodeDownload downloads a file from a supernode gRPC stream
func (a *cascadeAdapter) CascadeSupernodeDownload(
	ctx context.Context,
	in *CascadeSupernodeDownloadRequest,
	opts ...grpc.CallOption,
) (*CascadeSupernodeDownloadResponse, error) {

	ctx = net.AddCorrelationID(ctx)

	// 1. Open gRPC stream (server-stream)
	stream, err := a.client.Download(ctx, &cascade.DownloadRequest{
		ActionId: in.ActionID,
	}, opts...)
	if err != nil {
		a.logger.Error(ctx, "failed to create download stream",
			"action_id", in.ActionID, "error", err)
		return nil, err
	}

	// 2. Prepare destination file
	outFile, err := os.Create(in.OutputPath)
	if err != nil {
		a.logger.Error(ctx, "failed to create output file",
			"path", in.OutputPath, "error", err)
		return nil, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	var (
		bytesWritten int64
		chunkIndex   int
	)

	// 3. Receive streamed responses
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("stream recv: %w", err)
		}

		switch x := resp.ResponseType.(type) {

		// 3a. Progress / event message
		case *cascade.DownloadResponse_Event:
			a.logger.Info(ctx, "supernode event",
				"event_type", x.Event.EventType,
				"message", x.Event.Message,
				"action_id", in.ActionID)

			if in.EventLogger != nil {
				in.EventLogger(ctx, toSdkEvent(x.Event.EventType), x.Event.Message, event.EventData{
					event.KeyActionID:  in.ActionID,
					event.KeyEventType: x.Event.EventType,
					event.KeyMessage:   x.Event.Message,
				})
			}

		// 3b. Actual data chunk
		case *cascade.DownloadResponse_Chunk:
			data := x.Chunk.Data
			if len(data) == 0 {
				continue
			}
			if _, err := outFile.Write(data); err != nil {
				return nil, fmt.Errorf("write chunk: %w", err)
			}

			bytesWritten += int64(len(data))
			chunkIndex++

			a.logger.Debug(ctx, "received chunk",
				"chunk_index", chunkIndex,
				"chunk_size", len(data),
				"bytes_written", bytesWritten)
		}
	}

	a.logger.Info(ctx, "download complete",
		"bytes_written", bytesWritten,
		"path", in.OutputPath,
		"action_id", in.ActionID)

	return &CascadeSupernodeDownloadResponse{
		Success:    true,
		Message:    "artefact downloaded",
		OutputPath: in.OutputPath,
	}, nil
}

// toSdkEvent converts a supernode-side enum value into an internal SDK EventType.
func toSdkEvent(e cascade.SupernodeEventType) event.EventType {
	switch e {
	case cascade.SupernodeEventType_ACTION_RETRIEVED:
		return event.SupernodeActionRetrieved
	case cascade.SupernodeEventType_ACTION_FEE_VERIFIED:
		return event.SupernodeActionFeeVerified
	case cascade.SupernodeEventType_TOP_SUPERNODE_CHECK_PASSED:
		return event.SupernodeTopCheckPassed
	case cascade.SupernodeEventType_METADATA_DECODED:
		return event.SupernodeMetadataDecoded
	case cascade.SupernodeEventType_DATA_HASH_VERIFIED:
		return event.SupernodeDataHashVerified
	case cascade.SupernodeEventType_INPUT_ENCODED:
		return event.SupernodeInputEncoded
	case cascade.SupernodeEventType_SIGNATURE_VERIFIED:
		return event.SupernodeSignatureVerified
	case cascade.SupernodeEventType_RQID_GENERATED:
		return event.SupernodeRQIDGenerated
	case cascade.SupernodeEventType_RQID_VERIFIED:
		return event.SupernodeRQIDVerified
	case cascade.SupernodeEventType_ARTEFACTS_STORED:
		return event.SupernodeArtefactsStored
	case cascade.SupernodeEventType_ACTION_FINALIZED:
		return event.SupernodeActionFinalized
	case cascade.SupernodeEventType_ARTEFACTS_DOWNLOADED:
		return event.SupernodeArtefactsDownloaded
	default:
		return event.SupernodeUnknown
	}
}
