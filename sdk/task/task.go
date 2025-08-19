package task

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/LumeraProtocol/supernode/v2/pkg/errgroup"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"
	"github.com/LumeraProtocol/supernode/v2/sdk/net"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

type TaskType string

const (
	TaskTypeSense   TaskType = "SENSE"
	TaskTypeCascade TaskType = "CASCADE"
)

// EventCallback is a function that processes events from tasks
type EventCallback func(ctx context.Context, e event.Event)

// Task is the interface that all task types must implement
type Task interface {
	Run(ctx context.Context) error
}

// BaseTask contains common fields and methods for all task types
type BaseTask struct {
	TaskID   string
	ActionID string
	TaskType TaskType
	Action   lumera.Action

	// Dependencies
	keyring keyring.Keyring
	client  lumera.Client
	config  config.Config
	onEvent EventCallback
	logger  log.Logger
}

// EmitEvent creates and sends an event with the specified type and data
func (t *BaseTask) emitEvent(ctx context.Context, eventType event.EventType, data event.EventData) {
	if t.onEvent != nil {
		// Create event with the provided context
		e := event.NewEvent(ctx, eventType, t.TaskID, string(t.TaskType), t.ActionID, data)
		// Pass context to the callback
		t.onEvent(ctx, e)
	}
}

// logEvent is a helper function to log events with the task's logger
func (t *BaseTask) LogEvent(ctx context.Context, evt event.EventType, msg string, additionalInfo event.EventData) {
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
	t.emitEvent(ctx, evt, additionalInfo)
}

func (t *BaseTask) fetchSupernodes(ctx context.Context, height int64) (lumera.Supernodes, error) {
	sns, err := t.client.GetSupernodes(ctx, height)
	if err != nil {
		return nil, fmt.Errorf("fetch supernodes: %w", err)
	}

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

	return healthy, nil
}

// isServing pings the super-node once with a short timeout.
func (t *BaseTask) isServing(parent context.Context, sn lumera.Supernode) bool {
	ctx, cancel := context.WithTimeout(parent, connectionTimeout)
	defer cancel()

	client, err := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}).CreateClient(ctx, sn)
	if err != nil {
		logtrace.Info(ctx, "Failed to create client for supernode", logtrace.Fields{logtrace.FieldMethod: "isServing"})
		return false
	}
	defer client.Close(ctx)

	resp, err := client.HealthCheck(ctx)
	return err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING
}
