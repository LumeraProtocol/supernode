package task

import (
	"action/adapters/lumera"
	"action/config"
	"context"
	"errors"
	"fmt"
)

// SenseTask implements the Task interface for Sense operations
type SenseTask struct {
	BaseTask
	fileHash   string
	actionID   string
	filePath   string
	client     lumera.Client
	config     config.Config
	supernodes []lumera.Supernode
}

// NewSenseTask creates a new SenseTask
func NewSenseTask(
	taskID string,
	fileHash string,
	actionID string,
	filePath string,
	client lumera.Client,
	config config.Config,
) *SenseTask {
	return &SenseTask{
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

// Run executes the SenseTask
func (t *SenseTask) Run(ctx context.Context) error {
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

	// Store selected supernodes
	t.supernodes = supernodes

	// 3. Supernode Communication Phase
	// This will be implemented when the sense-specific requirements are defined

	// For now, just mark as completed
	t.Status = StatusCompleted
	return nil
}

// validateAction checks if the action exists and is in PENDING state
func (t *SenseTask) validateAction(ctx context.Context) (lumera.Action, error) {
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

// selectSupernodes selects multiple supernodes for sense operation
func (t *SenseTask) selectSupernodes(ctx context.Context, height int64) ([]lumera.Supernode, error) {
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
			if len(validSupernodes) >= t.config.SenseSupernodeCount {
				break
			}
		}
	}

	// Check if we have enough valid supernodes
	if len(validSupernodes) == 0 {
		return nil, errors.New("no valid supernodes available")
	}

	return validSupernodes, nil
}
