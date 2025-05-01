package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/net"

	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	UploadTimeout  = 60 * time.Second // Timeout for upload requests
	ConnectTimeout = 60 * time.Second // Timeout for connection requests
)

type CascadeTask struct {
	BaseTask
	FileHash   string
	FilePath   string
	SignedData string
}

// NewCascadeTask creates a new CascadeTask using a BaseTask plus cascade-specific parameters
func NewCascadeTask(
	base BaseTask,
	fileHash string,
	filePath string,
	signedData string,
) *CascadeTask {
	return &CascadeTask{
		BaseTask:   base,
		FileHash:   fileHash,
		FilePath:   filePath,
		SignedData: signedData,
	}
}

func (t *CascadeTask) Run(ctx context.Context) error {
	t.logger.Info(ctx, "Running cascade task",
		"taskID", t.TaskID,
		"actionID", t.ActionID)

	// Emit task started event with phase information
	t.EmitEvent(ctx, event.TaskStarted, map[string]interface{}{
		"total_phases": 3, // Action validation, supernode selection, upload
	})

	// Update status
	t.Status = StatusProcessing

	// 1. Action Validation Phase
	t.logger.Debug(ctx, "Starting action validation phase", "taskID", t.TaskID)
	t.EmitEvent(ctx, event.PhaseStarted, map[string]interface{}{
		"phase":        "action_validation",
		"phase_number": 1,
		"total_phases": 3,
	})

	action, err := t.validateAction(ctx)
	if err != nil {
		t.Status = StatusFailed
		t.Err = fmt.Errorf("action validation failed: %w", err)

		t.logger.Error(ctx, "Action validation failed",
			"taskID", t.TaskID,
			"actionID", t.ActionID,
			"error", err)

		t.EmitEvent(ctx, event.PhaseFailed, map[string]interface{}{
			"phase":        "action_validation",
			"phase_number": 1,
			"error":        err.Error(),
		})

		t.EmitEvent(ctx, event.TaskFailed, map[string]interface{}{
			"phase": "action_validation",
			"error": t.Err.Error(),
		})

		return t.Err
	}

	t.logger.Debug(ctx, "Action validation successful",
		"taskID", t.TaskID,
		"actionID", action.ID,
		"state", action.State)

	t.EmitEvent(ctx, event.PhaseCompleted, map[string]interface{}{
		"phase":         "action_validation",
		"phase_number":  1,
		"action_id":     action.ID,
		"action_state":  string(action.State),
		"action_height": action.Height,
	})

	// 2. Supernode Selection Phase
	t.logger.Debug(ctx, "Starting supernode selection phase", "taskID", t.TaskID)
	t.EmitEvent(ctx, event.PhaseStarted, map[string]interface{}{
		"phase":        "supernode_selection",
		"phase_number": 2,
		"total_phases": 3,
	})

	supernodes, err := t.client.GetSupernodes(ctx, action.Height)
	if err != nil {
		t.Status = StatusFailed
		t.Err = fmt.Errorf("supernode selection failed: %w", err)

		t.logger.Error(ctx, "Supernode selection failed",
			"taskID", t.TaskID,
			"error", err)

		t.EmitEvent(ctx, event.PhaseFailed, map[string]interface{}{
			"phase":        "supernode_selection",
			"phase_number": 2,
			"error":        err.Error(),
		})

		t.EmitEvent(ctx, event.TaskFailed, map[string]interface{}{
			"phase": "supernode_selection",
			"error": t.Err.Error(),
		})

		return t.Err
	}

	t.logger.Debug(ctx, "Supernode selection successful",
		"taskID", t.TaskID,
		"supernodeCount", len(supernodes))

	t.EmitEvent(ctx, event.PhaseCompleted, map[string]interface{}{
		"phase":           "supernode_selection",
		"phase_number":    2,
		"supernode_count": len(supernodes),
	})

	// 3. Create client factory
	t.logger.Debug(ctx, "Creating client factory", "taskID", t.TaskID)

	factoryConfig := net.FactoryConfig{
		LocalCosmosAddress:   t.config.Network.LocalCosmosAddress,
		DefaultSupernodePort: t.config.Network.DefaultSupernodePort,
	}
	clientFactory := net.NewClientFactory(
		ctx,
		t.logger,
		t.keyring,
		factoryConfig,
	)

	// Verify the file exists before we try to upload it
	t.logger.Debug(ctx, "Verifying file exists", "taskID", t.TaskID, "filePath", t.FilePath)
	fileInfo, err := os.Stat(t.FilePath)
	if err != nil {
		t.Status = StatusFailed
		t.Err = fmt.Errorf("failed to access file: %w", err)

		t.logger.Error(ctx, "Failed to access file",
			"taskID", t.TaskID,
			"filePath", t.FilePath,
			"error", err)

		t.EmitEvent(ctx, event.PhaseFailed, map[string]interface{}{
			"phase":        "upload",
			"phase_number": 3,
			"error":        t.Err.Error(),
		})

		t.EmitEvent(ctx, event.TaskFailed, map[string]interface{}{
			"phase": "file_access",
			"error": t.Err.Error(),
		})

		return t.Err
	}

	// 5. Create upload request
	filename := filepath.Base(t.FilePath)
	t.logger.Debug(ctx, "Creating upload request",
		"taskID", t.TaskID,
		"filename", filename,
		"actionID", t.ActionID,
		"fileHash", t.FileHash,
		"fileSize", fileInfo.Size(),
		"hasSignedData", t.SignedData != "")

	uploadRequest := &supernodeservice.UploadInputDataRequest{
		Filename:   filename,
		ActionID:   t.ActionID,
		DataHash:   t.FileHash,
		SignedData: t.SignedData,
		FilePath:   t.FilePath, // Pass the file path to the request
	}

	// 6. Upload Phase - Try each supernode until success
	t.logger.Debug(ctx, "Starting upload phase",
		"taskID", t.TaskID,
		"supernodeCount", len(supernodes))

	t.EmitEvent(ctx, event.PhaseStarted, map[string]interface{}{
		"phase":           "upload",
		"phase_number":    3,
		"total_phases":    3,
		"supernode_count": len(supernodes),
	})

	var lastErr error
	for i, sn := range supernodes {
		t.logger.Debug(ctx, "Attempting upload to supernode",
			"taskID", t.TaskID,
			"supernodeIndex", i+1,
			"supernodeAddress", sn.CosmosAddress,
			"endpoint", sn.GrpcEndpoint)

		// Emit supernode attempt event
		t.EmitEvent(ctx, event.SupernodeAttempt, map[string]interface{}{
			"phase":              "upload",
			"supernode_index":    i + 1,
			"total_supernodes":   len(supernodes),
			"supernode_address":  sn.CosmosAddress,
			"supernode_endpoint": sn.GrpcEndpoint,
		})

		// Try to upload to this supernode
		success, err := t.tryUploadToSupernode(ctx, clientFactory, sn, uploadRequest)
		if err != nil {
			lastErr = err

			t.logger.Error(ctx, "Failed to upload to supernode",
				"taskID", t.TaskID,
				"supernodeIndex", i+1,
				"supernodeAddress", sn.CosmosAddress,
				"error", err)

			// Emit supernode failed event
			t.EmitEvent(ctx, event.SupernodeFailed, map[string]interface{}{
				"phase":             "upload",
				"supernode_index":   i + 1,
				"supernode_address": sn.CosmosAddress,
				"error":             err.Error(),
			})

			continue
		}

		if success {
			t.logger.Info(ctx, "Successfully uploaded to supernode",
				"taskID", t.TaskID,
				"supernodeIndex", i+1,
				"supernodeAddress", sn.CosmosAddress)

			// Emit supernode success event
			t.EmitEvent(ctx, event.SupernodeSucceeded, map[string]interface{}{
				"phase":             "upload",
				"supernode_index":   i + 1,
				"supernode_address": sn.CosmosAddress,
			})

			// Emit phase completed event
			t.EmitEvent(ctx, event.PhaseCompleted, map[string]interface{}{
				"phase":                "upload",
				"phase_number":         3,
				"attempts":             i + 1,
				"successful_supernode": sn.CosmosAddress,
			})

			// Emit task completed event
			t.EmitEvent(ctx, event.TaskCompleted, map[string]interface{}{
				"total_phases":         3,
				"successful_supernode": sn.CosmosAddress,
			})

			// Update status and return success
			t.Status = StatusCompleted
			return nil
		}
	}

	// All supernodes failed
	t.Status = StatusFailed
	t.Err = fmt.Errorf("all supernodes failed: %w", lastErr)

	t.logger.Error(ctx, "All supernode upload attempts failed",
		"taskID", t.TaskID,
		"attempts", len(supernodes),
		"lastError", lastErr)

	// Emit phase failed event
	t.EmitEvent(ctx, event.PhaseFailed, map[string]interface{}{
		"phase":        "upload",
		"phase_number": 3,
		"attempts":     len(supernodes),
		"error":        t.Err.Error(),
	})

	// Emit task failed event
	t.EmitEvent(ctx, event.TaskFailed, map[string]interface{}{
		"phase": "upload",
		"error": t.Err.Error(),
	})

	return t.Err
}

// tryUploadToSupernode attempts to upload data to a single supernode
func (t *CascadeTask) tryUploadToSupernode(
	ctx context.Context,
	clientFactory *net.ClientFactory,
	supernode lumera.Supernode,
	request *supernodeservice.UploadInputDataRequest,
) (bool, error) {
	// Create a client for this supernode - only need to provide the context and supernode
	t.logger.Debug(ctx, "Creating client for supernode",
		"taskID", t.TaskID,
		"supernodeAddress", supernode.CosmosAddress)

	client, err := clientFactory.CreateClient(ctx, supernode)
	if err != nil {
		t.logger.Error(ctx, "Failed to create client for supernode",
			"taskID", t.TaskID,
			"supernodeAddress", supernode.CosmosAddress,
			"error", err)
		return false, fmt.Errorf("failed to create client for supernode %s: %w", supernode.CosmosAddress, err)
	}
	// Ensure connection is closed when we're done with this function
	defer client.Close(ctx)

	// Check if supernode is healthy
	t.logger.Debug(ctx, "Checking supernode health",
		"taskID", t.TaskID,
		"supernodeAddress", supernode.CosmosAddress)

	healthCtx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	healthResp, err := client.HealthCheck(healthCtx)
	cancel()

	if err != nil {
		t.logger.Error(ctx, "Health check failed for supernode",
			"taskID", t.TaskID,
			"supernodeAddress", supernode.CosmosAddress,
			"error", err)
		return false, fmt.Errorf("health check failed for supernode %s: %w", supernode.CosmosAddress, err)
	}

	if healthResp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.logger.Warn(ctx, "Supernode is not in serving state",
			"taskID", t.TaskID,
			"supernodeAddress", supernode.CosmosAddress,
			"state", healthResp.Status)
		return false, fmt.Errorf("supernode %s is not in serving state", supernode.CosmosAddress)
	}

	// Upload data to supernode
	t.logger.Debug(ctx, "Uploading data to supernode",
		"taskID", t.TaskID,
		"supernodeAddress", supernode.CosmosAddress,
		"filename", request.Filename,
		"filePath", request.FilePath)

	uploadCtx, cancel := context.WithTimeout(ctx, UploadTimeout)
	defer cancel()

	resp, err := client.UploadInputData(uploadCtx, request)
	if err != nil {
		t.logger.Error(ctx, "Upload failed to supernode",
			"taskID", t.TaskID,
			"supernodeAddress", supernode.CosmosAddress,
			"error", err)
		return false, fmt.Errorf("upload failed to supernode %s: %w", supernode.CosmosAddress, err)
	}

	// Check if the upload was successful based on response
	if !resp.Success {
		t.logger.Error(ctx, "Upload rejected by supernode",
			"taskID", t.TaskID,
			"supernodeAddress", supernode.CosmosAddress,
			"message", resp.Message)
		return false, fmt.Errorf("upload rejected by supernode %s: %s", supernode.CosmosAddress, resp.Message)
	}

	// Success!
	t.logger.Info(ctx, "Successfully uploaded data to supernode",
		"taskID", t.TaskID,
		"supernodeAddress", supernode.CosmosAddress,
		"message", resp.Message)
	return true, nil
}

// validateAction checks if the action exists and is in PENDING state
func (t *CascadeTask) validateAction(ctx context.Context) (lumera.Action, error) {
	t.logger.Debug(ctx, "Validating action",
		"taskID", t.TaskID,
		"actionID", t.ActionID)

	action, err := t.client.GetAction(ctx, t.ActionID)
	if err != nil {
		t.logger.Error(ctx, "Failed to get action",
			"taskID", t.TaskID,
			"actionID", t.ActionID,
			"error", err)
		return lumera.Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Check if action exists
	if action.ID == "" {
		t.logger.Error(ctx, "No action found with the specified ID",
			"taskID", t.TaskID,
			"actionID", t.ActionID)
		return lumera.Action{}, errors.New("no action found with the specified ID")
	}

	// Check action state
	if action.State != lumera.ACTION_STATE_PENDING {
		t.logger.Error(ctx, "Action is in invalid state",
			"taskID", t.TaskID,
			"actionID", t.ActionID,
			"state", action.State,
			"expectedState", lumera.ACTION_STATE_PENDING)
		return lumera.Action{}, fmt.Errorf("action is in %s state, expected PENDING", action.State)
	}

	t.logger.Debug(ctx, "Action validated successfully",
		"taskID", t.TaskID,
		"actionID", t.ActionID,
		"state", action.State,
		"height", action.Height)
	return action, nil
}
