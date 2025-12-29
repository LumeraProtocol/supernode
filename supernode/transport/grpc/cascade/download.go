package cascade

import (
	"fmt"
	"io"
	"os"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	tasks "github.com/LumeraProtocol/supernode/v2/pkg/task"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

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
	defer func() {
		if tmpDir == "" {
			return
		}
		if cerr := task.CleanupDownload(ctx, tmpDir); cerr != nil {
			logtrace.Warn(ctx, "cleanup of tmp dir failed", logtrace.Fields{"tmp_dir": tmpDir, logtrace.FieldError: cerr.Error()})
		}
	}()
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
			if err := stream.Send(&pb.DownloadResponse{ResponseType: &pb.DownloadResponse_Chunk{Chunk: &pb.DataChunk{Data: buf[:n]}}}); err != nil {
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

	logtrace.Debug(ctx, "download stream completed", fields)
	return nil
}
