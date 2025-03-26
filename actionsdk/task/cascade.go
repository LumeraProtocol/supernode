// File: task/cascade.go
package task

import (
	"action/adapters/lumera"
	"action/config"
	"action/net"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// CascadeTask implements the Task interface for Cascade operations
type CascadeTask struct {
	BaseTask
	fileHash string
	actionID string
	filePath string
	client   lumera.Client
	config   config.Config
}

// NewCascadeTask creates a new CascadeTask
func NewCascadeTask(
	taskID string,
	fileHash string,
	actionID string,
	filePath string,
	client lumera.Client,
	config config.Config,
) *CascadeTask {
	return &CascadeTask{
		BaseTask: BaseTask{
			TaskID: taskID,
			Status: StatusPending,
		},
		fileHash: fileHash,
		actionID: actionID,
		filePath: filePath,
		client:   client,
		config:   config,
	}
}

// Run executes the CascadeTask
func (t *CascadeTask) Run(ctx context.Context) error {
	// Update status
	t.Status = StatusProcessing

	// 1. Action Validation Phase
	action, err := t.validateAction(ctx)
	if err != nil {
		t.Status = StatusFailed
		t.Err = fmt.Errorf("action validation failed: %w", err)
		return t.Err
	}

	// 2. Supernode Selection Phase
	supernodes, err := t.selectSupernodes(ctx, action.Height)
	if err != nil {
		t.Status = StatusFailed
		t.Err = fmt.Errorf("supernode selection failed: %w", err)
		return t.Err
	}

	// 3. Create client factory using global config
	clientFactory := net.NewClientFactory(t.config)

	// TODO : Verify this part on the supernode side

	// 4. Read file content
	// fileData, err := os.ReadFile(t.filePath)
	// if err != nil {
	// 	t.Status = StatusFailed
	// 	t.Err = fmt.Errorf("failed to read file: %w", err)
	// 	return t.Err
	// }

	// 5. Create upload request with correct field names matching the protobuf definition
	uploadRequest := &cascade.UploadInputDataRequest{
		Filename: filepath.Base(t.filePath), // Extract filename from the path
		ActionId: t.actionID,
		DataHash: t.fileHash, // Changed from FileHash to DataHash
		// Data:     fileData,   // Add the actual file data
	}

	// 6. Try each supernode until success
	var lastErr error
	for _, sn := range supernodes {
		// Try to upload to this supernode
		success, err := t.tryUploadToSupernode(ctx, clientFactory, sn, uploadRequest)
		if err != nil {
			lastErr = err
			continue
		}

		if success {
			// Success!
			t.Status = StatusCompleted
			return nil
		}
	}

	// All supernodes failed
	t.Status = StatusFailed
	t.Err = fmt.Errorf("all supernodes failed: %w", lastErr)
	return t.Err
}

// tryUploadToSupernode attempts to upload data to a single supernode
func (t *CascadeTask) tryUploadToSupernode(
	ctx context.Context,
	clientFactory *net.ClientFactory,
	supernode lumera.Supernode,
	request *cascade.UploadInputDataRequest,
) (bool, error) {
	// Create a client for this supernode
	client, err := clientFactory.CreateClient(ctx, supernode)
	if err != nil {
		return false, fmt.Errorf("failed to create client for supernode %s: %w", supernode.CosmosAddress, err)
	}
	// Ensure connection is closed when we're done with this function
	defer client.Close()

	// Check if supernode is healthy
	healthCtx, cancel := context.WithTimeout(ctx, time.Duration(t.config.TimeoutSeconds)*time.Second)
	healthResp, err := client.HealthCheck(healthCtx)
	cancel()

	if err != nil {
		return false, fmt.Errorf("health check failed for supernode %s: %w", supernode.CosmosAddress, err)
	}

	if healthResp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return false, fmt.Errorf("supernode %s is not in serving state", supernode.CosmosAddress)
	}

	// Upload data to supernode
	uploadCtx, cancel := context.WithTimeout(ctx, time.Duration(t.config.TimeoutSeconds)*time.Second)
	defer cancel()

	_, err = client.UploadInputData(uploadCtx, request)
	if err != nil {
		return false, fmt.Errorf("upload failed to supernode %s: %w", supernode.CosmosAddress, err)
	}

	// Success!
	return true, nil
}

// validateAction checks if the action exists and is in PENDING state
func (t *CascadeTask) validateAction(ctx context.Context) (lumera.Action, error) {
	action, err := t.client.GetAction(ctx, t.actionID)
	if err != nil {
		return lumera.Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Check if action exists
	if action.ID == "" {
		return lumera.Action{}, errors.New("no action found with the specified ID")
	}

	// Check action state
	if action.State != lumera.ACTION_STATE_PENDING {
		return lumera.Action{}, fmt.Errorf("action is in %s state, expected PENDING", action.State)
	}

	return action, nil
}

// selectSupernodes selects supernodes for cascade operation
func (t *CascadeTask) selectSupernodes(ctx context.Context, height int64) ([]lumera.Supernode, error) {
	// Get top supernodes
	supernodes, err := t.client.GetSupernodes(ctx, height)
	if err != nil {
		return nil, fmt.Errorf("failed to get supernodes: %w", err)
	}

	// Filter valid supernodes
	var validSupernodes []lumera.Supernode
	for _, sn := range supernodes {
		if sn.State == lumera.SUPERNODE_STATE_ACTIVE && sn.GrpcEndpoint != "" {
			validSupernodes = append(validSupernodes, sn)
			if len(validSupernodes) >= t.config.CascadeSupernodeCount {
				break
			}
		}
	}

	if len(validSupernodes) == 0 {
		return nil, errors.New("no valid supernodes available")
	}

	return validSupernodes, nil
}
