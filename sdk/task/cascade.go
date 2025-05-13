package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/net"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	registrationTimeout = 120 * time.Second // Timeout for registration requests
	connectionTimeout   = 10 * time.Second  // Timeout for connection requests
)

type CascadeTask struct {
	BaseTask
	filePath string
	actionId string
}

// NewCascadeTask creates a new CascadeTask using a BaseTask plus cascade-specific parameters
func NewCascadeTask(base BaseTask, filePath string, actionId string) *CascadeTask {
	return &CascadeTask{
		BaseTask: base,
		filePath: filePath,
		actionId: actionId,
	}
}

// Run executes the full cascade‐task lifecycle.
func (t *CascadeTask) Run(ctx context.Context) error {
	t.logEvent(ctx, event.TaskStarted, "Running cascade task", nil)

	action, err := t.fetchAndValidateAction(ctx, t.ActionID)
	if err != nil {
		return t.fail(ctx, event.TaskProgressActionVerificationFailed, err)
	}
	t.logEvent(ctx, event.TaskProgressActionVerified, "Action verified.", nil)

	supernodes, err := t.fetchSupernodes(ctx, action.Height)
	if err != nil {
		return t.fail(ctx, event.TaskProgressSupernodesUnavailable, err)
	}
	t.logEvent(ctx, event.TaskProgressSupernodesFound, "Supernodes found.", map[string]interface{}{
		"count": len(supernodes),
	})

	if err := t.registerWithSupernodes(ctx, supernodes); err != nil {
		return t.fail(ctx, event.TaskProgressRegistrationFailure, err)
	}
	t.logEvent(ctx, event.TaskCompleted, "Cascade task completed successfully", nil)
	t.Status = StatusCompleted

	return nil
}

// fetchAndValidateAction checks if the action exists and is in PENDING state
func (t *CascadeTask) fetchAndValidateAction(ctx context.Context, actionID string) (lumera.Action, error) {
	action, err := t.client.GetAction(ctx, actionID)
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

func (t *CascadeTask) fetchSupernodes(ctx context.Context, height int64) (lumera.Supernodes, error) {
	sns, err := t.client.GetSupernodes(ctx, height)
	if err != nil {
		return nil, fmt.Errorf("fetch supernodes: %w", err)
	}
	t.logger.Info(ctx, "Supernodes fetched", "count", len(sns))

	if len(sns) == 0 {
		return nil, errors.New("no supernodes found")
	}

	if len(sns) > 10 {
		sns = sns[:10]
	}

	// Keep only SERVING nodes (done in parallel – keeps latency flat)
	healthy := make(lumera.Supernodes, 0, len(sns))
	eg, ctx := errgroup.WithContext(ctx)
	mu := sync.Mutex{}

	for _, sn := range sns {
		sn := sn
		eg.Go(func() error {
			if t.isServing(ctx, sn) {
				mu.Lock()
				healthy = append(healthy, sn)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("health-check goroutines: %w", err)
	}

	if len(healthy) == 0 {
		return nil, errors.New("no healthy supernodes found")
	}
	t.logger.Info(ctx, "Healthy supernodes", "count", len(healthy))

	return healthy, nil
}

// isServing pings the super-node once with a short timeout.
func (t *CascadeTask) isServing(parent context.Context, sn lumera.Supernode) bool {
	ctx, cancel := context.WithTimeout(parent, connectionTimeout)
	defer cancel()

	client, err := net.NewClientFactory(ctx, t.logger, t.keyring, net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
	}).CreateClient(ctx, sn)
	if err != nil {
		logtrace.Info(ctx, "Failed to create client for supernode", logtrace.Fields{
			logtrace.FieldMethod: "isServing"})
		return false
	}
	defer client.Close(ctx)

	resp, err := client.HealthCheck(ctx)
	return err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING
}

func (t *CascadeTask) registerWithSupernodes(ctx context.Context, supernodes lumera.Supernodes) error {
	factoryCfg := net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
	}
	clientFactory := net.NewClientFactory(ctx, t.logger, t.keyring, factoryCfg)

	req := &supernodeservice.CascadeSupernodeRegisterRequest{
		FilePath: t.filePath,
		ActionID: t.ActionID,
		TaskId:   t.TaskID,
	}

	var lastErr error
	for idx, sn := range supernodes {
		if err := t.attemptRegistration(ctx, idx, sn, clientFactory, req); err != nil {
			lastErr = err
			continue
		}
		return nil // success
	}

	return fmt.Errorf("failed to upload to all supernodes: %w", lastErr)
}
func (t *CascadeTask) attemptRegistration(ctx context.Context, index int, sn lumera.Supernode, factory *net.ClientFactory, req *supernodeservice.CascadeSupernodeRegisterRequest) error {

	t.logEvent(ctx, event.TaskProgressRegistrationInProgress, "attempting registration with supernode", map[string]interface{}{
		"supernode": sn.GrpcEndpoint, "sn-address": sn.CosmosAddress, "iteration": index + 1})

	client, err := factory.CreateClient(ctx, sn)
	if err != nil {
		return fmt.Errorf("create client %s: %w", sn.CosmosAddress, err)
	}
	defer client.Close(ctx)

	uploadCtx, cancel := context.WithTimeout(ctx, registrationTimeout)
	defer cancel()

	req.EventLogger = func(ctx context.Context, evt event.EventType, msg string, data map[string]interface{}) {
		t.logEvent(ctx, evt, msg, data)
	}
	resp, err := client.RegisterCascade(uploadCtx, req)
	if err != nil {
		return fmt.Errorf("upload to %s: %w", sn.CosmosAddress, err)
	}
	if !resp.Success {
		return fmt.Errorf("upload rejected by %s: %s", sn.CosmosAddress, resp.Message)
	}

	txhash := CleanTxHash(resp.TxHash)
	t.logEvent(ctx, event.TxhasReceived, "txhash received", map[string]interface{}{
		"txhash":    txhash,
		"supernode": sn.CosmosAddress,
	})

	t.logger.Info(ctx, "upload OK", "taskID", t.TaskID, "address", sn.CosmosAddress)
	return nil
}

// logEvent writes a structured log entry **and** emits the SDK event.
func (t *CascadeTask) logEvent(ctx context.Context, evt event.EventType, msg string, additionalInfo map[string]interface{}) {
	// Base fields that are always present
	kvs := []interface{}{
		"taskID", t.TaskID,
		"actionID", t.ActionID,
	}

	// Merge additional fields
	for k, v := range additionalInfo {
		kvs = append(kvs, k, v)
	}

	t.logger.Info(ctx, msg, kvs...)
	t.EmitEvent(ctx, evt, additionalInfo)
}

func (t *CascadeTask) fail(ctx context.Context, failureEvent event.EventType, err error) error {
	t.Status = StatusFailed
	t.Err = err

	t.logger.Error(ctx, "Task failed", "taskID", t.TaskID, "actionID", t.ActionID, "error", err)

	t.EmitEvent(ctx, failureEvent, map[string]interface{}{
		"error": err.Error(),
	})
	t.EmitEvent(ctx, event.TaskFailed, map[string]interface{}{
		"error": err.Error(),
	})

	return err
}

func CleanTxHash(input string) string {
	// Split by colon and get the last part
	parts := strings.Split(input, ":")
	if len(parts) <= 1 {
		return input
	}

	// Return the last part with spaces trimmed
	return strings.TrimSpace(parts[len(parts)-1])
}
