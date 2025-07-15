package supernodeservice

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/gen/supernode"
	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/net"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/log"

	"google.golang.org/grpc"
)

type cascadeAdapter struct {
	client       cascade.CascadeServiceClient
	statusClient supernode.SupernodeServiceClient
	logger       log.Logger
}

func NewCascadeAdapter(ctx context.Context, conn *grpc.ClientConn, logger log.Logger) CascadeServiceClient {
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Debug(ctx, "Creating cascade service adapter")

	return &cascadeAdapter{
		client:       cascade.NewCascadeServiceClient(conn),
		statusClient: supernode.NewSupernodeServiceClient(conn),
		logger:       logger,
	}
}

// calculateOptimalChunkSize returns an optimal chunk size based on file size
// to balance throughput and memory usage
func calculateOptimalChunkSize(fileSize int64) int {
	const (
		minChunkSize = 64 * 1024    // 64 KB minimum
		maxChunkSize = 4 * 1024 * 1024  // 4 MB maximum for 1GB+ files
		smallFileThreshold = 1024 * 1024  // 1 MB
		mediumFileThreshold = 50 * 1024 * 1024  // 50 MB
		largeFileThreshold = 500 * 1024 * 1024  // 500 MB
	)

	var chunkSize int
	
	switch {
	case fileSize <= smallFileThreshold:
		// For small files (up to 1MB), use 64KB chunks
		chunkSize = minChunkSize
	case fileSize <= mediumFileThreshold:
		// For medium files (1MB-50MB), use 256KB chunks
		chunkSize = 256 * 1024
	case fileSize <= largeFileThreshold:
		// For large files (50MB-500MB), use 1MB chunks
		chunkSize = 1024 * 1024
	default:
		// For very large files (500MB+), use 4MB chunks for optimal throughput
		chunkSize = maxChunkSize
	}
	
	// Ensure chunk size is within bounds
	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	
	return chunkSize
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

	// Define adaptive chunk size based on file size
	chunkSize := calculateOptimalChunkSize(totalBytes)
	
	a.logger.Debug(ctx, "Calculated optimal chunk size", "fileSize", totalBytes, "chunkSize", chunkSize)

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

func (a *cascadeAdapter) GetSupernodeStatus(ctx context.Context) (SupernodeStatusresponse, error) {
	resp, err := a.statusClient.GetStatus(ctx, &supernode.StatusRequest{})
	if err != nil {
		a.logger.Error(ctx, "Failed to get supernode status", "error", err)
		return SupernodeStatusresponse{}, fmt.Errorf("failed to get supernode status: %w", err)
	}

	a.logger.Debug(ctx, "Supernode status retrieved", "status", resp)

	return *toSdkSupernodeStatus(resp), nil
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
		ActionId:  in.ActionID,
		Signature: in.Signature,
	}, opts...)
	if err != nil {
		a.logger.Error(ctx, "failed to create download stream", "action_id", in.ActionID, "error", err)
		return nil, err
	}

	// 2. Prepare destination file
	// Create directory structure if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(in.OutputPath), 0755); err != nil {
		a.logger.Error(ctx, "failed to create output directory", "path", filepath.Dir(in.OutputPath), "error", err)
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	outFile, err := os.Create(in.OutputPath)
	if err != nil {
		a.logger.Error(ctx, "failed to create output file", "path", in.OutputPath, "error", err)
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
			a.logger.Info(ctx, "supernode event", "event_type", x.Event.EventType, "message", x.Event.Message, "action_id", in.ActionID)

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

			a.logger.Debug(ctx, "received chunk", "chunk_index", chunkIndex, "chunk_size", len(data), "bytes_written", bytesWritten)
		}
	}

	a.logger.Info(ctx, "download complete", "bytes_written", bytesWritten, "path", in.OutputPath, "action_id", in.ActionID)

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

func toSdkSupernodeStatus(resp *supernode.StatusResponse) *SupernodeStatusresponse {
	result := &SupernodeStatusresponse{}

	// Convert CPU data
	if resp.Cpu != nil {
		result.CPU.Usage = resp.Cpu.Usage
		result.CPU.Remaining = resp.Cpu.Remaining
	}

	// Convert Memory data
	if resp.Memory != nil {
		result.Memory.Total = resp.Memory.Total
		result.Memory.Used = resp.Memory.Used
		result.Memory.Available = resp.Memory.Available
		result.Memory.UsedPerc = resp.Memory.UsedPerc
	}

	// Convert Services data
	result.Services = make([]ServiceTasks, 0, len(resp.Services))
	for _, service := range resp.Services {
		result.Services = append(result.Services, ServiceTasks{
			ServiceName: service.ServiceName,
			TaskIDs:     service.TaskIds,
			TaskCount:   service.TaskCount,
		})
	}

	// Convert AvailableServices data
	result.AvailableServices = make([]string, len(resp.AvailableServices))
	copy(result.AvailableServices, resp.AvailableServices)

	return result
}
