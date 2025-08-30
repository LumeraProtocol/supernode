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
	logtrace.Info(ctx, "client streaming request to upload cascade input data received", fields)

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

				logtrace.Info(ctx, "received data chunk", logtrace.Fields{
					"chunk_size":        len(x.Chunk.Data),
					"total_size_so_far": totalSize,
				})
			}
		case *pb.RegisterRequest_Metadata:
			// Store metadata - this should be the final message
			metadata = x.Metadata
			logtrace.Info(ctx, "received metadata", logtrace.Fields{
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
	logtrace.Info(ctx, "metadata received from action-sdk", fields)

	// Ensure all data is written to disk before calculating hash
	if err := tempFile.Sync(); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to sync temp file", fields)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	hash := hasher.Sum(nil)
	hashHex := hex.EncodeToString(hash)
	fields[logtrace.FieldHashHex] = hashHex
	logtrace.Info(ctx, "final BLAKE3 hash generated", fields)

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

	logtrace.Info(ctx, "cascade registration completed successfully", fields)
	return nil
}

func (server *ActionServer) Download(req *pb.DownloadRequest, stream pb.CascadeService_DownloadServer) error {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "Download",
		logtrace.FieldModule:   "CascadeActionServer",
		logtrace.FieldActionID: req.GetActionId(),
	}

	ctx := stream.Context()
	logtrace.Info(ctx, "download request received from client", fields)

	task := server.factory.NewCascadeRegistrationTask()

	// Verify signature if provided
	if req.GetSignature() != "" {
		// Cast to concrete type to access helper method
		if cascadeTask, ok := task.(*cascadeService.CascadeRegistrationTask); ok {
			err := cascadeTask.VerifyDownloadSignature(ctx, req.GetActionId(), req.GetSignature())
			if err != nil {
				fields[logtrace.FieldError] = err.Error()
				logtrace.Error(ctx, "signature verification failed", fields)
				return fmt.Errorf("signature verification failed: %w", err)
			}
		} else {
			logtrace.Error(ctx, "unable to cast task to CascadeRegistrationTask", fields)
			return fmt.Errorf("unable to verify signature: task type assertion failed")
		}
	}

	var restoredFilePath string
	var tmpDir string

	// Ensure tmpDir is cleaned up even if errors occur after retrieval
	defer func() {
		if tmpDir != "" {
			if err := task.CleanupDownload(ctx, tmpDir); err != nil {
				logtrace.Error(ctx, "error cleaning up the tmp dir", logtrace.Fields{logtrace.FieldError: err.Error()})
			} else {
				logtrace.Info(ctx, "tmp dir has been cleaned up", logtrace.Fields{"tmp_dir": tmpDir})
			}
		}
	}()

	err := task.Download(ctx, &cascadeService.DownloadRequest{
		ActionID: req.GetActionId(),
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
	logtrace.Info(ctx, "streaming artefact file in chunks", fields)

	restoredFile, err := readFileContentsInChunks(restoredFilePath)
	if err != nil {
		logtrace.Error(ctx, "failed to read restored file", logtrace.Fields{
			logtrace.FieldError: err.Error(),
		})
		return err
	}
	logtrace.Info(ctx, "file has been read in chunks", fields)

	// Calculate optimal chunk size based on file size
	chunkSize := calculateOptimalChunkSize(int64(len(restoredFile)))
	logtrace.Info(ctx, "calculated optimal chunk size for download", logtrace.Fields{
		"file_size":  len(restoredFile),
		"chunk_size": chunkSize,
	})

	// Split and stream the file using adaptive chunk size
	for i := 0; i < len(restoredFile); i += chunkSize {
		end := i + chunkSize
		if end > len(restoredFile) {
			end = len(restoredFile)
		}

		err := stream.Send(&pb.DownloadResponse{
			ResponseType: &pb.DownloadResponse_Chunk{
				Chunk: &pb.DataChunk{
					Data: restoredFile[i:end],
				},
			},
		})

		if err != nil {
			logtrace.Error(ctx, "failed to stream chunk", logtrace.Fields{
				logtrace.FieldError: err.Error(),
			})
			return err
		}
	}

	// Cleanup is handled in deferred block above

	logtrace.Info(ctx, "completed streaming all chunks", fields)
	return nil
}

func readFileContentsInChunks(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 1024*1024)
	var fileBytes []byte

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			// Process chunk
			fileBytes = append(fileBytes, buf[:n]...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("chunked read failed: %w", readErr)
		}
	}

	return fileBytes, nil
}
