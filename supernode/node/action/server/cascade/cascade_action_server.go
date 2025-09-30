package cascade

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade"

	"google.golang.org/grpc"
)

type ActionServer struct {
	pb.UnimplementedCascadeServiceServer
	factory cascadeService.CascadeServiceFactory
}

// NewCascadeActionServer creates a new CascadeActionServer with injected service
func NewCascadeActionServer(factory cascadeService.CascadeServiceFactory) *ActionServer {
	return &ActionServer{factory: factory}
}

// calculateOptimalChunkSize returns an optimal chunk size based on file size
// to balance throughput and memory usage
func calculateOptimalChunkSize(fileSize int64) int {
	const (
		minChunkSize        = 64 * 1024         // 64 KB minimum
		maxChunkSize        = 4 * 1024 * 1024   // 4 MB maximum for 1GB+ files
		smallFileThreshold  = 1024 * 1024       // 1 MB
		mediumFileThreshold = 50 * 1024 * 1024  // 50 MB
		largeFileThreshold  = 500 * 1024 * 1024 // 500 MB
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

func (server *ActionServer) Desc() *grpc.ServiceDesc {
	return &pb.CascadeService_ServiceDesc
}

func (server *ActionServer) Register(stream pb.CascadeService_RegisterServer) error {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "Register",
		logtrace.FieldModule: "CascadeActionServer",
	}

	ctx := stream.Context()
	logtrace.Debug(ctx, "client streaming request to upload cascade input data received", fields)

	const maxFileSize = 1 * 1024 * 1024 * 1024 // 1GB limit

	var (
		metadata  *pb.Metadata
		totalSize int
	)

	hasher, tempFile, tempFilePath, err := initializeHasherAndTempFile()
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to initialize hasher and temp file", fields)
		return fmt.Errorf("initializing hasher and temp file: %w", err)
	}
	defer func(tempFile *os.File) {
		err := tempFile.Close()
		if err != nil && !errors.Is(err, os.ErrClosed) {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Warn(ctx, "error closing temp file", fields)
		}
	}(tempFile)

	// Process incoming stream
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// End of stream
			break
		}
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "error receiving stream data", fields)
			return fmt.Errorf("failed to receive stream data: %w", err)
		}

		// Check which type of message we received
		switch x := req.RequestType.(type) {
		case *pb.RegisterRequest_Chunk:
			if x.Chunk != nil {

				// hash the chunks
				_, err := hasher.Write(x.Chunk.Data)
				if err != nil {
					fields[logtrace.FieldError] = err.Error()
					logtrace.Error(ctx, "failed to write chunk to hasher", fields)
					return fmt.Errorf("hashing error: %w", err)
				}

				// write chunks to the file
				_, err = tempFile.Write(x.Chunk.Data)
				if err != nil {
					fields[logtrace.FieldError] = err.Error()
					logtrace.Error(ctx, "failed to write chunk to file", fields)
					return fmt.Errorf("file write error: %w", err)
				}
				totalSize += len(x.Chunk.Data)

				// Validate total size doesn't exceed limit
				if totalSize > maxFileSize {
					fields[logtrace.FieldError] = "file size exceeds 1GB limit"
					fields["total_size"] = totalSize
					logtrace.Error(ctx, "upload rejected: file too large", fields)
					return fmt.Errorf("file size %d exceeds maximum allowed size of 1GB", totalSize)
				}

				logtrace.Debug(ctx, "received data chunk", logtrace.Fields{
					"chunk_size":        len(x.Chunk.Data),
					"total_size_so_far": totalSize,
				})
			}
		case *pb.RegisterRequest_Metadata:
			// Store metadata - this should be the final message
			metadata = x.Metadata
			logtrace.Debug(ctx, "received metadata", logtrace.Fields{
				"task_id":   metadata.TaskId,
				"action_id": metadata.ActionId,
			})
		}
	}

	// Verify we received metadata
	if metadata == nil {
		logtrace.Error(ctx, "no metadata received in stream", fields)
		return fmt.Errorf("no metadata received")
	}
	fields[logtrace.FieldTaskID] = metadata.GetTaskId()
	fields[logtrace.FieldActionID] = metadata.GetActionId()
	logtrace.Debug(ctx, "metadata received from action-sdk", fields)

	// Ensure all data is written to disk before calculating hash
	if err := tempFile.Sync(); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to sync temp file", fields)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	hash := hasher.Sum(nil)
	hashHex := hex.EncodeToString(hash)
	fields[logtrace.FieldHashHex] = hashHex
	logtrace.Debug(ctx, "final BLAKE3 hash generated", fields)

	targetPath, err := replaceTempDirWithTaskDir(metadata.GetTaskId(), tempFilePath, tempFile)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to replace temp dir with task dir", fields)
		return fmt.Errorf("failed to replace temp dir with task dir: %w", err)
	}

	// Process the complete data
	task := server.factory.NewCascadeRegistrationTask()
	err = task.Register(ctx, &cascadeService.RegisterRequest{
		TaskID:   metadata.TaskId,
		ActionID: metadata.ActionId,
		DataHash: hash,
		DataSize: totalSize,
		FilePath: targetPath,
	}, func(resp *cascadeService.RegisterResponse) error {
		grpcResp := &pb.RegisterResponse{
			EventType: pb.SupernodeEventType(resp.EventType),
			Message:   resp.Message,
			TxHash:    resp.TxHash,
		}
		if err := stream.Send(grpcResp); err != nil {
			logtrace.Error(ctx, "failed to send response to client", logtrace.Fields{
				logtrace.FieldError: err.Error(),
			})
			return err
		}
		return nil
	})

	if err != nil {
		logtrace.Error(ctx, "registration task failed", logtrace.Fields{
			logtrace.FieldError: err.Error(),
		})
		return fmt.Errorf("registration failed: %w", err)
	}

	logtrace.Debug(ctx, "cascade registration completed successfully", fields)
	return nil
}

func (server *ActionServer) Download(req *pb.DownloadRequest, stream pb.CascadeService_DownloadServer) error {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "Download",
		logtrace.FieldModule:   "CascadeActionServer",
		logtrace.FieldActionID: req.GetActionId(),
	}

	ctx := stream.Context()
	logtrace.Debug(ctx, "download request received from client", fields)

	task := server.factory.NewCascadeRegistrationTask()

	// Authorization is enforced inside the task based on metadata.Public.
	// If public, signature is skipped; if private, signature is required.

	var restoredFilePath string
	var tmpDir string

	// Ensure tmpDir is cleaned up even if errors occur after retrieval
	defer func() {
		if tmpDir != "" {
			if err := task.CleanupDownload(ctx, tmpDir); err != nil {
				logtrace.Error(ctx, "error cleaning up the tmp dir", logtrace.Fields{logtrace.FieldError: err.Error()})
			} else {
				logtrace.Debug(ctx, "tmp dir has been cleaned up", logtrace.Fields{"tmp_dir": tmpDir})
			}
		}
	}()

	err := task.Download(ctx, &cascadeService.DownloadRequest{
		ActionID:  req.GetActionId(),
		Signature: req.GetSignature(),
	}, func(resp *cascadeService.DownloadResponse) error {
		grpcResp := &pb.DownloadResponse{
			ResponseType: &pb.DownloadResponse_Event{
				Event: &pb.DownloadEvent{
					EventType: pb.SupernodeEventType(resp.EventType),
					Message:   resp.Message,
				},
			},
		}

		if resp.FilePath != "" {
			restoredFilePath = resp.FilePath
			tmpDir = resp.DownloadedDir
		}

		return stream.Send(grpcResp)
	})

	if err != nil {
		logtrace.Error(ctx, "error occurred during download process", logtrace.Fields{
			logtrace.FieldError: err.Error(),
		})
		return err
	}

	if restoredFilePath == "" {
		logtrace.Error(ctx, "no artefact file retrieved", fields)
		return fmt.Errorf("no artefact to stream")
	}
	logtrace.Debug(ctx, "streaming artefact file in chunks", fields)

	// Open the restored file and stream directly from disk to avoid buffering entire file in memory
	f, err := os.Open(restoredFilePath)
	if err != nil {
		logtrace.Error(ctx, "failed to open restored file", logtrace.Fields{logtrace.FieldError: err.Error()})
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		logtrace.Error(ctx, "failed to stat restored file", logtrace.Fields{logtrace.FieldError: err.Error()})
		return err
	}

	// Calculate optimal chunk size based on file size
	chunkSize := calculateOptimalChunkSize(fi.Size())
	logtrace.Debug(ctx, "calculated optimal chunk size for download", logtrace.Fields{
		"file_size":  fi.Size(),
		"chunk_size": chunkSize,
	})

	// Pre-read first chunk to avoid any delay between SERVE_READY and first data
	buf := make([]byte, chunkSize)
	n, readErr := f.Read(buf)
	if readErr != nil && readErr != io.EOF {
		return fmt.Errorf("chunked read failed: %w", readErr)
	}

	// Announce: file is ready to be served to the client (right before first data)
	if err := stream.Send(&pb.DownloadResponse{
		ResponseType: &pb.DownloadResponse_Event{
			Event: &pb.DownloadEvent{
				EventType: pb.SupernodeEventType_SERVE_READY,
				Message:   "File available for download",
			},
		},
	}); err != nil {
		logtrace.Error(ctx, "failed to send serve-ready event", logtrace.Fields{logtrace.FieldError: err.Error()})
		return err
	}

	// Send pre-read first chunk if available
	if n > 0 {
		if err := stream.Send(&pb.DownloadResponse{
			ResponseType: &pb.DownloadResponse_Chunk{
				Chunk: &pb.DataChunk{Data: buf[:n]},
			},
		}); err != nil {
			logtrace.Error(ctx, "failed to stream first chunk", logtrace.Fields{logtrace.FieldError: err.Error()})
			return err
		}
	}

	// If EOF after first read, we're done
	if readErr == io.EOF {
		logtrace.Debug(ctx, "completed streaming all chunks", fields)
		return nil
	}

	// Continue streaming remaining chunks
	for {
		n, readErr = f.Read(buf)
		if n > 0 {
			if err := stream.Send(&pb.DownloadResponse{
				ResponseType: &pb.DownloadResponse_Chunk{
					Chunk: &pb.DataChunk{Data: buf[:n]},
				},
			}); err != nil {
				logtrace.Error(ctx, "failed to stream chunk", logtrace.Fields{logtrace.FieldError: err.Error()})
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("chunked read failed: %w", readErr)
		}
	}

	// Cleanup is handled in deferred block above

	logtrace.Debug(ctx, "completed streaming all chunks", fields)
	return nil
}
