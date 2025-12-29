package cascade

import (
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	tasks "github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"lukechampine.com/blake3"
)

func (server *ActionServer) Register(stream pb.CascadeService_RegisterServer) error {
	fields := logtrace.Fields{
		logtrace.FieldMethod: "Register",
		logtrace.FieldModule: "CascadeActionServer",
	}

	ctx := stream.Context()
	logtrace.Info(ctx, "register: stream open", fields)

	const maxFileSize = 1 * 1024 * 1024 * 1024 // 1GB limit

	var (
		metadata     *pb.Metadata
		totalSize    int
		uploadHandle *tasks.Handle
	)

	hasher, tempFile, tempDir, tempFilePath, err := initializeHasherAndTempFile()
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to initialize hasher and temp file", fields)
		return fmt.Errorf("initializing hasher and temp file: %w", err)
	}
	defer func() {
		if tempDir == "" {
			return
		}
		if err := os.RemoveAll(tempDir); err != nil {
			fields[logtrace.FieldError] = err.Error()
			fields["temp_dir"] = tempDir
			logtrace.Warn(ctx, "failed to cleanup upload temp dir", fields)
		}
	}()
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
				// Keep chunk logs at debug to avoid verbosity
				logtrace.Debug(ctx, "received data chunk", logtrace.Fields{"chunk_size": len(x.Chunk.Data), "total_size_so_far": totalSize})
			}
		case *pb.RegisterRequest_Metadata:
			metadata = x.Metadata
			// Set correlation ID for the rest of the flow
			ctx = logtrace.CtxWithCorrelationID(ctx, metadata.ActionId)
			fields[logtrace.FieldTaskID] = metadata.GetTaskId()
			fields[logtrace.FieldActionID] = metadata.GetActionId()
			logtrace.Info(ctx, "register: metadata received", fields)
			actionID := metadata.GetActionId()
			if actionID == "" {
				return status.Error(codes.InvalidArgument, "missing action_id")
			}
			// Start live task tracking on first metadata (covers remaining stream and processing).
			// Track by ActionID to prevent duplicate in-flight uploads for the same action.
			if uploadHandle == nil {
				h, herr := tasks.StartUniqueWith(server.tracker, ctx, serviceCascadeUpload, actionID, server.uploadTimeout)
				if herr != nil {
					if errors.Is(herr, tasks.ErrAlreadyRunning) {
						return status.Errorf(codes.AlreadyExists, "upload already in progress for %s", actionID)
					}
					return herr
				}
				uploadHandle = h
				defer uploadHandle.End(ctx)
			}
		}
	}

	if metadata == nil {
		logtrace.Error(ctx, "no metadata received in stream", fields)
		return fmt.Errorf("no metadata received")
	}
	fields[logtrace.FieldTaskID] = metadata.GetTaskId()
	fields[logtrace.FieldActionID] = metadata.GetActionId()
	logtrace.Info(ctx, "register: stream upload complete", fields)

	if err := tempFile.Sync(); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to sync temp file", fields)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	hash := hasher.Sum(nil)
	hashHex := hex.EncodeToString(hash)
	fields[logtrace.FieldHashHex] = hashHex
	logtrace.Info(ctx, "register: hash computed", fields)

	targetPath, err := replaceTempDirWithTaskDir(metadata.GetTaskId(), tempFilePath, tempFile)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to replace temp dir with task dir", fields)
		return fmt.Errorf("failed to replace temp dir with task dir: %w", err)
	}

	task := server.factory.NewCascadeRegistrationTask()
	logtrace.Info(ctx, "register: task start", fields)
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
		// Mirror event to Info logs for high-level tracing
		logtrace.Info(ctx, "register: event", logtrace.Fields{"event_type": resp.EventType, "message": resp.Message, logtrace.FieldTxHash: resp.TxHash, logtrace.FieldActionID: metadata.ActionId, logtrace.FieldTaskID: metadata.TaskId})
		return nil
	})
	if err != nil {
		logtrace.Error(ctx, "registration task failed", logtrace.Fields{logtrace.FieldError: err.Error()})
		return fmt.Errorf("registration failed: %w", err)
	}
	logtrace.Info(ctx, "register: task ok", fields)
	return nil
}

// initializeHasherAndTempFile prepares a hasher and a temporary file to stream upload data into.
func initializeHasherAndTempFile() (hash.Hash, *os.File, string, string, error) {
	// Create a temp directory for the upload
	tmpDir, err := os.MkdirTemp("", "supernode-upload-*")
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("create temp dir: %w", err)
	}

	// Create a file within the temp directory
	filePath := filepath.Join(tmpDir, "data.bin")
	f, err := os.Create(filePath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, nil, "", "", fmt.Errorf("create temp file: %w", err)
	}

	// Create a BLAKE3 hasher (32 bytes output)
	hasher := blake3.New(32, nil)
	return hasher, f, tmpDir, filePath, nil
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
