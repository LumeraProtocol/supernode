package supernodeservice

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"

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

const maxFileSize = 1 * 1024 * 1024 * 1024 // 1GB limit

func (a *cascadeAdapter) CascadeSupernodeRegister(ctx context.Context, in *CascadeSupernodeRegisterRequest, opts ...grpc.CallOption) (*CascadeSupernodeRegisterResponse, error) {
	// Create a cancelable context for phased timers (no correlation IDs)
	baseCtx := ctx
	phaseCtx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	// Create the client stream
	stream, err := a.client.Register(phaseCtx, opts...)
	if err != nil {
		a.logger.Error(ctx, "Failed to create register stream", "error", err)
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "stream_open",
				event.KeyError:    err.Error(),
			})
		}
		return nil, err
	}

	// Open the file for reading
	file, err := os.Open(in.FilePath)
	if err != nil {
		a.logger.Error(ctx, "Failed to open file", "filePath", in.FilePath, "error", err)
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "file_open",
				event.KeyError:    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file stats for progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		a.logger.Error(ctx, "Failed to get file stats", "filePath", in.FilePath, "error", err)
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "file_stat",
				event.KeyError:    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to get file stats: %w", err)
	}
	totalBytes := fileInfo.Size()

	// Validate file size before starting upload
	if totalBytes > maxFileSize {
		a.logger.Error(ctx, "File exceeds maximum size limit",
			"filePath", in.FilePath,
			"fileSize", totalBytes,
			"maxSize", maxFileSize)
		return nil, fmt.Errorf("file size %d bytes exceeds maximum allowed size of 1GB", totalBytes)
	}

	// Define adaptive chunk size based on file size
	chunkSize := calculateOptimalChunkSize(totalBytes)

	a.logger.Debug(ctx, "Calculated optimal chunk size", "fileSize", totalBytes, "chunkSize", chunkSize)

	// Keep track of how much data we've processed
	bytesRead := int64(0)
	chunkIndex := 0
	buffer := make([]byte, chunkSize)

	// Emit upload started event
	if in.EventLogger != nil {
		estChunks := (totalBytes + int64(chunkSize) - 1) / int64(chunkSize)
		in.EventLogger(baseCtx, event.SDKUploadStarted, "Upload started",
			event.EventData{
				event.KeyTaskID:     in.TaskId,
				event.KeyActionID:   in.ActionID,
				event.KeyBytesTotal: totalBytes,
				event.KeyChunkSize:  chunkSize,
				event.KeyEstChunks:  estChunks,
			})
	}

	uploadStart := time.Now()

	// Start upload phase timer
	uploadTimer := time.AfterFunc(cascadeUploadTimeout, func() {
		a.logger.Error(baseCtx, "Upload phase timeout reached; cancelling stream")
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "timeout",
				event.KeyError:    "upload phase timeout",
			})
		}
		cancel()
	})

	// Read and send data in chunks (upload phase)
	for {
		// Read a chunk from the file
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			a.logger.Error(ctx, "Failed to read file chunk", "chunkIndex", chunkIndex, "error", err)
			if in.EventLogger != nil {
				in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
					event.KeyTaskID:     in.TaskId,
					event.KeyActionID:   in.ActionID,
					event.KeyReason:     "read_error",
					event.KeyChunkIndex: chunkIndex,
					event.KeyError:      err.Error(),
				})
			}
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
			if in.EventLogger != nil {
				in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
					event.KeyTaskID:     in.TaskId,
					event.KeyActionID:   in.ActionID,
					event.KeyReason:     "send_error",
					event.KeyChunkIndex: chunkIndex,
					event.KeyError:      err.Error(),
				})
			}
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
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "send_metadata",
				event.KeyError:    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to send metadata: %w", err)
	}

	a.logger.Debug(ctx, "Sent metadata", "TaskId", in.TaskId, "ActionID", in.ActionID)

	if err := stream.CloseSend(); err != nil {
		a.logger.Error(ctx, "Failed to close stream and receive response", "TaskId", in.TaskId, "ActionID", in.ActionID, "error", err)
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKUploadFailed, "Upload failed", event.EventData{
				event.KeyTaskID:   in.TaskId,
				event.KeyActionID: in.ActionID,
				event.KeyReason:   "close_send",
				event.KeyError:    err.Error(),
			})
		}
		return nil, fmt.Errorf("failed to receive response: %w", err)
	}

	// Upload phase completed; stop its timer
	if uploadTimer != nil {
		uploadTimer.Stop()
	}

	// Emit upload completed with throughput metrics
	if in.EventLogger != nil {
		elapsed := time.Since(uploadStart).Seconds()
		mb := float64(bytesRead) / (1024.0 * 1024.0)
		avg := 0.0
		if elapsed > 0 {
			avg = mb / elapsed
		}
		in.EventLogger(baseCtx, event.SDKUploadCompleted, "Upload completed",
			event.EventData{
				event.KeyTaskID:         in.TaskId,
				event.KeyActionID:       in.ActionID,
				event.KeyBytesTotal:     totalBytes,
				event.KeyChunks:         chunkIndex,
				event.KeyElapsedSeconds: elapsed,
				event.KeyThroughputMBS:  avg,
			})
	}

	// Processing phase timer starts now (waiting for server streamed responses)
	processingTimer := time.AfterFunc(cascadeProcessingTimeout, func() {
		a.logger.Error(baseCtx, "Processing phase timeout reached; cancelling stream")
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKProcessingTimeout, "Processing timed out", event.EventData{event.KeyTaskID: in.TaskId, event.KeyActionID: in.ActionID})
		}
		cancel()
	})
	defer func() {
		if processingTimer != nil {
			processingTimer.Stop()
		}
	}()

	// Emit processing started
	if in.EventLogger != nil {
		in.EventLogger(baseCtx, event.SDKProcessingStarted, "Processing started", event.EventData{event.KeyTaskID: in.TaskId, event.KeyActionID: in.ActionID})
	}

	// Handle streaming responses from supernode
	var finalResp *cascade.RegisterResponse
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Distinguish timeout phase for clearer error messages
			if phaseCtx.Err() != nil {
				// At this point, upload is finished; classify as processing timeout/cancel
				if phaseCtx.Err() == context.DeadlineExceeded || phaseCtx.Err() == context.Canceled {
					return nil, fmt.Errorf("processing timed out or cancelled: %w", phaseCtx.Err())
				}
			}
			if in.EventLogger != nil {
				in.EventLogger(baseCtx, event.SDKProcessingFailed, "Processing failed",
					event.EventData{event.KeyTaskID: in.TaskId, event.KeyActionID: in.ActionID, event.KeyError: err.Error()})
			}
			return nil, fmt.Errorf("failed to receive server response: %w", err)
		}

		// Log the streamed progress update
		a.logger.Info(ctx, "Supernode progress update received", "event_type", resp.EventType, "message", resp.Message, "tx_hash", resp.TxHash, "task_id", in.TaskId, "action_id", in.ActionID)

		if in.EventLogger != nil {
			edata := event.EventData{
				event.KeyEventType: resp.EventType,
				event.KeyMessage:   resp.Message,
				event.KeyTxHash:    resp.TxHash,
				event.KeyTaskID:    in.TaskId,
				event.KeyActionID:  in.ActionID,
			}
			// Extract success rate if provided in message format: "... success_rate=NN.NN%"
			if resp.EventType == cascade.SupernodeEventType_ARTEFACTS_STORED {
				if rate, ok := parseSuccessRate(resp.Message); ok {
					edata[event.KeySuccessRate] = rate
				}
			}
			in.EventLogger(ctx, toSdkEventWithMessage(resp.EventType, resp.Message), resp.Message, edata)
		}

		// Optionally capture the final response
		if resp.TxHash != "" {
			finalResp = resp
		}
	}

	if finalResp == nil {
		// If context was cancelled due to timer, surface a more specific error
		if phaseCtx.Err() != nil {
			return nil, fmt.Errorf("processing timed out or cancelled before final response: %w", phaseCtx.Err())
		}
		if in.EventLogger != nil {
			in.EventLogger(baseCtx, event.SDKProcessingFailed, "Processing failed: missing final response", event.EventData{event.KeyTaskID: in.TaskId, event.KeyActionID: in.ActionID})
		}
		return nil, fmt.Errorf("no final response with tx_hash received")
	}

	return &CascadeSupernodeRegisterResponse{
		Success: true,
		Message: finalResp.Message,
		TxHash:  finalResp.TxHash,
	}, nil
}

func (a *cascadeAdapter) GetSupernodeStatus(ctx context.Context) (SupernodeStatusresponse, error) {
	// Gate P2P metrics via context option to keep API backward compatible
	req := &supernode.StatusRequest{IncludeP2PMetrics: includeP2PMetrics(ctx)}
	resp, err := a.statusClient.GetStatus(ctx, req)
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

	// Use provided context as-is (no correlation IDs). Add watchdogs:
	// - idle timer: reset on every received message (event or chunk).
	// - max timer: hard cap for one attempt.
	phaseCtx, phaseCancel := context.WithCancel(ctx)
	defer phaseCancel()

	// 1. Open gRPC stream (server-stream)
	stream, err := a.client.Download(phaseCtx, &cascade.DownloadRequest{
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
        bytesWritten   int64
        chunkIndex     int
        startedEmitted bool
    )

	// 3. Receive streamed responses with liveness watchdog
	// Start with a generous prep idle timeout; tighten after first message
	currentIdle := downloadPrepIdleTimeout
	idleTimer := time.AfterFunc(currentIdle, func() {
		a.logger.Error(ctx, "download idle timeout; cancelling stream", "action_id", in.ActionID)
		phaseCancel()
	})
	defer idleTimer.Stop()
	maxTimer := time.AfterFunc(downloadMaxTimeout, func() {
		a.logger.Error(ctx, "download max timeout; cancelling stream", "action_id", in.ActionID)
		phaseCancel()
	})
	defer maxTimer.Stop()
	start := time.Now()
	lastActivity := start
	firstMsg := false

	// 3. Receive streamed responses
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Classify timeouts for clearer upstream handling
			if phaseCtx.Err() != nil {
				sinceLast := time.Since(lastActivity)
				sinceStart := time.Since(start)
				switch {
				case sinceLast >= downloadIdleTimeout:
					return nil, fmt.Errorf("download idle timeout: %w", context.DeadlineExceeded)
				case sinceStart >= downloadMaxTimeout:
					return nil, fmt.Errorf("download overall timeout: %w", context.DeadlineExceeded)
				}
			}
			return nil, fmt.Errorf("stream recv: %w", err)
		}

		switch x := resp.ResponseType.(type) {

		// 3a. Progress / event message
		case *cascade.DownloadResponse_Event:
			// On first message, tighten idle window for active transfer
			if !firstMsg {
				firstMsg = true
				currentIdle = downloadIdleTimeout
			}
			if idleTimer != nil {
				idleTimer.Reset(currentIdle)
			}
			lastActivity = time.Now()
			a.logger.Info(ctx, "supernode event", "event_type", x.Event.EventType, "message", x.Event.Message, "action_id", in.ActionID)

			if in.EventLogger != nil {
				in.EventLogger(ctx, toSdkEvent(x.Event.EventType), x.Event.Message, event.EventData{
					event.KeyTaskID:    in.TaskID,
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
            if !startedEmitted {
                if in.EventLogger != nil {
                    in.EventLogger(ctx, event.SDKDownloadStarted, "Download started", event.EventData{event.KeyActionID: in.ActionID})
                }
                startedEmitted = true
            }
            if _, err := outFile.Write(data); err != nil {
                return nil, fmt.Errorf("write chunk: %w", err)
            }

			bytesWritten += int64(len(data))
			chunkIndex++
			if !firstMsg {
				firstMsg = true
				currentIdle = downloadIdleTimeout
			}
			if idleTimer != nil {
				idleTimer.Reset(currentIdle)
			}
			lastActivity = time.Now()
			a.logger.Debug(ctx, "received chunk", "chunk_index", chunkIndex, "chunk_size", len(data), "bytes_written", bytesWritten)
		}
	}

	a.logger.Info(ctx, "download complete", "bytes_written", bytesWritten, "path", in.OutputPath, "action_id", in.ActionID)

    if in.EventLogger != nil {
        in.EventLogger(ctx, event.SDKDownloadCompleted, "Download completed", event.EventData{event.KeyActionID: in.ActionID, event.KeyOutputPath: in.OutputPath})
    }
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
    case cascade.SupernodeEventType_NETWORK_RETRIEVE_STARTED:
        return event.SupernodeNetworkRetrieveStarted
    case cascade.SupernodeEventType_DECODE_COMPLETED:
        return event.SupernodeDecodeCompleted
    case cascade.SupernodeEventType_SERVE_READY:
        return event.SupernodeServeReady
	case cascade.SupernodeEventType_FINALIZE_SIMULATED:
		return event.SupernodeFinalizeSimulated
	case cascade.SupernodeEventType_FINALIZE_SIMULATION_FAILED:
		return event.SupernodeFinalizeSimulationFailed
	default:
		return event.SupernodeUnknown
	}
}

// toSdkEventWithMessage extends event mapping using message content for finer granularity
func toSdkEventWithMessage(e cascade.SupernodeEventType, msg string) event.EventType {
	// Detect finalize simulation pass piggybacked on RQID_VERIFIED
	if e == cascade.SupernodeEventType_RQID_VERIFIED && msg == "finalize action simulation passed" {
		return event.SupernodeFinalizeSimulated
	}
	return toSdkEvent(e)
}

var rateRe = regexp.MustCompile(`success_rate=([0-9]+(?:\.[0-9]+)?)%`)

func parseSuccessRate(msg string) (float64, bool) {
	m := rateRe.FindStringSubmatch(msg)
	if len(m) != 2 {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func toSdkSupernodeStatus(resp *supernode.StatusResponse) *SupernodeStatusresponse {
	result := &SupernodeStatusresponse{}
	result.Version = resp.Version
	result.UptimeSeconds = resp.UptimeSeconds

	// Convert Resources data
	if resp.Resources != nil {
		// Convert CPU data
		if resp.Resources.Cpu != nil {
			result.Resources.CPU.UsagePercent = resp.Resources.Cpu.UsagePercent
			result.Resources.CPU.Cores = resp.Resources.Cpu.Cores
		}

		// Convert Memory data
		if resp.Resources.Memory != nil {
			result.Resources.Memory.TotalGB = resp.Resources.Memory.TotalGb
			result.Resources.Memory.UsedGB = resp.Resources.Memory.UsedGb
			result.Resources.Memory.AvailableGB = resp.Resources.Memory.AvailableGb
			result.Resources.Memory.UsagePercent = resp.Resources.Memory.UsagePercent
		}

		// Convert Storage data
		result.Resources.Storage = make([]StorageInfo, 0, len(resp.Resources.StorageVolumes))
		for _, storage := range resp.Resources.StorageVolumes {
			result.Resources.Storage = append(result.Resources.Storage, StorageInfo{
				Path:           storage.Path,
				TotalBytes:     storage.TotalBytes,
				UsedBytes:      storage.UsedBytes,
				AvailableBytes: storage.AvailableBytes,
				UsagePercent:   storage.UsagePercent,
			})
		}

		// Copy hardware summary
		result.Resources.HardwareSummary = resp.Resources.HardwareSummary
	}

	// Convert RunningTasks data
	result.RunningTasks = make([]ServiceTasks, 0, len(resp.RunningTasks))
	for _, service := range resp.RunningTasks {
		result.RunningTasks = append(result.RunningTasks, ServiceTasks{
			ServiceName: service.ServiceName,
			TaskIDs:     service.TaskIds,
			TaskCount:   service.TaskCount,
		})
	}

	// Convert RegisteredServices data
	result.RegisteredServices = make([]string, len(resp.RegisteredServices))
	copy(result.RegisteredServices, resp.RegisteredServices)

	// Convert Network data
	if resp.Network != nil {
		result.Network.PeersCount = resp.Network.PeersCount
		result.Network.PeerAddresses = make([]string, len(resp.Network.PeerAddresses))
		copy(result.Network.PeerAddresses, resp.Network.PeerAddresses)
	}

	// Copy rank and IP address
	result.Rank = resp.Rank
	result.IPAddress = resp.IpAddress

	// Map optional P2P metrics
	if resp.P2PMetrics != nil {
		// DHT metrics
		if resp.P2PMetrics.DhtMetrics != nil {
			// Store success recent
			for _, p := range resp.P2PMetrics.DhtMetrics.StoreSuccessRecent {
				result.P2PMetrics.DhtMetrics.StoreSuccessRecent = append(result.P2PMetrics.DhtMetrics.StoreSuccessRecent, struct {
					TimeUnix    int64
					Requests    int32
					Successful  int32
					SuccessRate float64
				}{
					TimeUnix:    p.TimeUnix,
					Requests:    p.Requests,
					Successful:  p.Successful,
					SuccessRate: p.SuccessRate,
				})
			}
			// Batch retrieve recent
			for _, p := range resp.P2PMetrics.DhtMetrics.BatchRetrieveRecent {
				result.P2PMetrics.DhtMetrics.BatchRetrieveRecent = append(result.P2PMetrics.DhtMetrics.BatchRetrieveRecent, struct {
					TimeUnix     int64
					Keys         int32
					Required     int32
					FoundLocal   int32
					FoundNetwork int32
					DurationMS   int64
				}{
					TimeUnix:     p.TimeUnix,
					Keys:         p.Keys,
					Required:     p.Required,
					FoundLocal:   p.FoundLocal,
					FoundNetwork: p.FoundNetwork,
					DurationMS:   p.DurationMs,
				})
			}
			result.P2PMetrics.DhtMetrics.HotPathBannedSkips = resp.P2PMetrics.DhtMetrics.HotPathBannedSkips
			result.P2PMetrics.DhtMetrics.HotPathBanIncrements = resp.P2PMetrics.DhtMetrics.HotPathBanIncrements
		}

		// Network handle metrics
		if resp.P2PMetrics.NetworkHandleMetrics != nil {
			if result.P2PMetrics.NetworkHandleMetrics == nil {
				result.P2PMetrics.NetworkHandleMetrics = map[string]struct {
					Total   int64
					Success int64
					Failure int64
					Timeout int64
				}{}
			}
			for k, v := range resp.P2PMetrics.NetworkHandleMetrics {
				result.P2PMetrics.NetworkHandleMetrics[k] = struct {
					Total   int64
					Success int64
					Failure int64
					Timeout int64
				}{
					Total:   v.Total,
					Success: v.Success,
					Failure: v.Failure,
					Timeout: v.Timeout,
				}
			}
		}

		// Conn pool metrics
		if resp.P2PMetrics.ConnPoolMetrics != nil {
			if result.P2PMetrics.ConnPoolMetrics == nil {
				result.P2PMetrics.ConnPoolMetrics = map[string]int64{}
			}
			for k, v := range resp.P2PMetrics.ConnPoolMetrics {
				result.P2PMetrics.ConnPoolMetrics[k] = v
			}
		}

		// Ban list
		for _, b := range resp.P2PMetrics.BanList {
			result.P2PMetrics.BanList = append(result.P2PMetrics.BanList, struct {
				ID            string
				IP            string
				Port          uint32
				Count         int32
				CreatedAtUnix int64
				AgeSeconds    int64
			}{
				ID:            b.Id,
				IP:            b.Ip,
				Port:          b.Port,
				Count:         b.Count,
				CreatedAtUnix: b.CreatedAtUnix,
				AgeSeconds:    b.AgeSeconds,
			})
		}

		// Database
		if resp.P2PMetrics.Database != nil {
			result.P2PMetrics.Database.P2PDBSizeMB = resp.P2PMetrics.Database.P2PDbSizeMb
			result.P2PMetrics.Database.P2PDBRecordsCount = resp.P2PMetrics.Database.P2PDbRecordsCount
		}

		// Disk
		if resp.P2PMetrics.Disk != nil {
			result.P2PMetrics.Disk.AllMB = resp.P2PMetrics.Disk.AllMb
			result.P2PMetrics.Disk.UsedMB = resp.P2PMetrics.Disk.UsedMb
			result.P2PMetrics.Disk.FreeMB = resp.P2PMetrics.Disk.FreeMb
		}
	}

	return result
}
