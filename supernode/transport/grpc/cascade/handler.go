package cascade

import (
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	tasks "github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"lukechampine.com/blake3"
)

type ActionServer struct {
	pb.UnimplementedCascadeServiceServer
	factory         cascadeService.CascadeServiceFactory
	tracker         tasks.Tracker
	uploadTimeout   time.Duration
	downloadTimeout time.Duration
}

const (
	serviceCascadeUpload   = "cascade.upload"
	serviceCascadeDownload = "cascade.download"
)

// NewCascadeActionServer creates a new CascadeActionServer with injected service and tracker
func NewCascadeActionServer(factory cascadeService.CascadeServiceFactory, tracker tasks.Tracker, uploadTO, downloadTO time.Duration) *ActionServer {
	if uploadTO <= 0 {
		uploadTO = 30 * time.Minute
	}
	if downloadTO <= 0 {
		downloadTO = 30 * time.Minute
	}
	return &ActionServer{factory: factory, tracker: tracker, uploadTimeout: uploadTO, downloadTimeout: downloadTO}
}

// calculateOptimalChunkSize returns an optimal chunk size based on file size
// to balance throughput and memory usage

var (
	startedTask bool
	handle      *tasks.Handle
)

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
		chunkSize = minChunkSize
	case fileSize <= mediumFileThreshold:
		chunkSize = 256 * 1024
	case fileSize <= largeFileThreshold:
		chunkSize = 1024 * 1024
	default:
		chunkSize = maxChunkSize
	}

	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	if chunkSize > maxChunkSize {
		chunkSize = maxChunkSize
	}
	return chunkSize
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

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "error receiving stream data", fields)
			return fmt.Errorf("failed to receive stream data: %w", err)
		}

		switch x := req.RequestType.(type) {
		case *pb.RegisterRequest_Chunk:
			if x.Chunk != nil {
				if _, err := hasher.Write(x.Chunk.Data); err != nil {
					fields[logtrace.FieldError] = err.Error()
					logtrace.Error(ctx, "failed to write chunk to hasher", fields)
					return fmt.Errorf("hashing error: %w", err)
				}
				if _, err := tempFile.Write(x.Chunk.Data); err != nil {
					fields[logtrace.FieldError] = err.Error()
					logtrace.Error(ctx, "failed to write chunk to file", fields)
					return fmt.Errorf("file write error: %w", err)
				}
				totalSize += len(x.Chunk.Data)
				if totalSize > maxFileSize {
					fields[logtrace.FieldError] = "file size exceeds 1GB limit"
					fields["total_size"] = totalSize
					logtrace.Error(ctx, "upload rejected: file too large", fields)
					return fmt.Errorf("file size %d exceeds maximum allowed size of 1GB", totalSize)
				}
				logtrace.Debug(ctx, "received data chunk", logtrace.Fields{"chunk_size": len(x.Chunk.Data), "total_size_so_far": totalSize})
			}
		case *pb.RegisterRequest_Metadata:
			metadata = x.Metadata
			logtrace.Debug(ctx, "received metadata", logtrace.Fields{"task_id": metadata.TaskId, "action_id": metadata.ActionId})
			// Start live task tracking on first metadata (covers remaining stream and processing)
			if !startedTask {
				startedTask = true
				handle = tasks.StartWith(server.tracker, ctx, serviceCascadeUpload, metadata.ActionId, server.uploadTimeout)
				defer handle.End(ctx)
			}
		}
	}

	if metadata == nil {
		logtrace.Error(ctx, "no metadata received in stream", fields)
		return fmt.Errorf("no metadata received")
	}
	fields[logtrace.FieldTaskID] = metadata.GetTaskId()
	fields[logtrace.FieldActionID] = metadata.GetActionId()
	logtrace.Debug(ctx, "metadata received from action-sdk", fields)

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
			logtrace.Error(ctx, "failed to send response to client", logtrace.Fields{logtrace.FieldError: err.Error()})
			return err
		}
		return nil
	})
	if err != nil {
		logtrace.Error(ctx, "registration task failed", logtrace.Fields{logtrace.FieldError: err.Error()})
		return fmt.Errorf("registration failed: %w", err)
	}
	logtrace.Debug(ctx, "cascade registration completed successfully", fields)
	return nil
}

func (server *ActionServer) Download(req *pb.DownloadRequest, stream pb.CascadeService_DownloadServer) error {
	ctx := stream.Context()
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "Download",
		logtrace.FieldModule:   "CascadeActionServer",
		logtrace.FieldActionID: req.GetActionId(),
	}
	logtrace.Debug(ctx, "download request received", fields)

	// Start live task tracking for the entire download RPC (including file streaming)
	dlHandle := tasks.StartWith(server.tracker, ctx, serviceCascadeDownload, req.GetActionId(), server.downloadTimeout)
	defer dlHandle.End(ctx)

	// Prepare to capture decoded file path from task events
	var decodedFilePath string
	var tmpDir string

	task := server.factory.NewCascadeRegistrationTask()
	// Run cascade task Download; stream events back to client
	err := task.Download(ctx, &cascadeService.DownloadRequest{ActionID: req.GetActionId(), Signature: req.GetSignature()}, func(resp *cascadeService.DownloadResponse) error {
		// Forward event to gRPC client
		evt := &pb.DownloadResponse{
			ResponseType: &pb.DownloadResponse_Event{
				Event: &pb.DownloadEvent{
					EventType: pb.SupernodeEventType(resp.EventType),
					Message:   resp.Message,
				},
			},
		}
		if sendErr := stream.Send(evt); sendErr != nil {
			return sendErr
		}
		// Capture decode-completed info for streaming
		if resp.EventType == cascadeService.SupernodeEventTypeDecodeCompleted {
			decodedFilePath = resp.FilePath
			tmpDir = resp.DownloadedDir
		}
		return nil
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "download task failed", fields)
		return fmt.Errorf("download task failed: %w", err)
	}

	if decodedFilePath == "" {
		logtrace.Warn(ctx, "decode completed without file path", fields)
		return nil
	}

	// Notify client that server is ready to stream the file
	logtrace.Debug(ctx, "download: serve ready", logtrace.Fields{"event_type": cascadeService.SupernodeEventTypeServeReady, logtrace.FieldActionID: req.GetActionId()})
	if err := stream.Send(&pb.DownloadResponse{ResponseType: &pb.DownloadResponse_Event{Event: &pb.DownloadEvent{EventType: pb.SupernodeEventType_SERVE_READY, Message: "Serve ready"}}}); err != nil {
		return fmt.Errorf("send serve-ready: %w", err)
	}

	// Stream file content in chunks
	fi, err := os.Stat(decodedFilePath)
	if err != nil {
		return fmt.Errorf("stat decoded file: %w", err)
	}
	chunkSize := calculateOptimalChunkSize(fi.Size())
	f, err := os.Open(decodedFilePath)
	if err != nil {
		return fmt.Errorf("open decoded file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, chunkSize)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if err := stream.Send(&pb.DownloadResponse{ResponseType: &pb.DownloadResponse_Chunk{Chunk: &pb.DataChunk{Data: append([]byte(nil), buf[:n]...)}}}); err != nil {
				return fmt.Errorf("send chunk: %w", err)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read decoded file: %w", rerr)
		}
	}

	// Cleanup temp directory if provided
	if tmpDir != "" {
		if cerr := task.CleanupDownload(ctx, tmpDir); cerr != nil {
			logtrace.Warn(ctx, "cleanup of tmp dir failed", logtrace.Fields{"tmp_dir": tmpDir, logtrace.FieldError: cerr.Error()})
		}
	}

	logtrace.Debug(ctx, "download stream completed", fields)
	return nil
}

// initializeHasherAndTempFile prepares a hasher and a temporary file to stream upload data into.
func initializeHasherAndTempFile() (hash.Hash, *os.File, string, error) {
	// Create a temp directory for the upload
	tmpDir, err := os.MkdirTemp("", "supernode-upload-*")
	if err != nil {
		return nil, nil, "", fmt.Errorf("create temp dir: %w", err)
	}

	// Create a file within the temp directory
	filePath := filepath.Join(tmpDir, "data.bin")
	f, err := os.Create(filePath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create temp file: %w", err)
	}

	// Create a BLAKE3 hasher (32 bytes output)
	hasher := blake3.New(32, nil)
	return hasher, f, filePath, nil
}

// replaceTempDirWithTaskDir moves the uploaded file into a task-scoped directory
// and returns the new absolute path.
func replaceTempDirWithTaskDir(taskID, tempFilePath string, tempFile *os.File) (string, error) {
	// Ensure data is flushed
	_ = tempFile.Sync()
	// Close now; deferred close may run later and is safe to ignore
	_ = tempFile.Close()

	// Create a stable target directory under OS temp
	targetDir := filepath.Join(os.TempDir(), "supernode", "uploads", taskID)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", fmt.Errorf("create task dir: %w", err)
	}

	newPath := filepath.Join(targetDir, filepath.Base(tempFilePath))
	if err := os.Rename(tempFilePath, newPath); err != nil {
		return "", fmt.Errorf("move uploaded file: %w", err)
	}

	// Attempt to cleanup the original temp directory
	_ = os.RemoveAll(filepath.Dir(tempFilePath))
	return newPath, nil
}
