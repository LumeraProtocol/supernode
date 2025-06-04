package cascade

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"google.golang.org/grpc"
	"lukechampine.com/blake3"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	cascadeService "github.com/LumeraProtocol/supernode/supernode/services/cascade"
)

type ActionServer struct {
	pb.UnimplementedCascadeServiceServer
	factory cascadeService.TaskFactory
}

// NewCascadeActionServer creates a new CascadeActionServer with injected service
func NewCascadeActionServer(factory cascadeService.TaskFactory) *ActionServer {
	return &ActionServer{factory: factory}
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

	hash := hasher.Sum(nil)

	b64Hash := base64.StdEncoding.EncodeToString(hash)
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
		DataHash: b64Hash,
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

func initializeHasherAndTempFile() (*blake3.Hasher, *os.File, string, error) {
	hasher := blake3.New(32, nil)

	tempFilePath := filepath.Join(os.TempDir(), fmt.Sprintf("cascade-upload-%d.tmp", os.Getpid()))
	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("could not create temp file: %w", err)
	}

	return hasher, tempFile, tempFilePath, nil
}

func replaceTempDirWithTaskDir(taskID, tempFilePath string, tempFile *os.File) (targetPath string, err error) {
	if err := tempFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	targetDir := filepath.Join(os.TempDir(), taskID)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("could not create task directory: %w", err)
	}
	targetPath = filepath.Join(targetDir, fmt.Sprintf("uploaded-%s.dat", taskID))
	if err := os.Rename(tempFilePath, targetPath); err != nil {
		return "", fmt.Errorf("could not move file to final location: %w", err)
	}

	return targetPath, nil
}
