package task

import (
	"context"
	"errors"
	"fmt"
	"os"

	sdkmath "cosmossdk.io/math"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
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

// Package-level thresholds and tuning
const (
	// Minimum available storage required on any volume (bytes)
	minStorageThresholdBytes uint64 = 50 * 1024 * 1024 * 1024 // 50 GB
	// Upload requires free RAM to be at least 8x the file size
	uploadRAMMultiplier uint64 = 8
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
	// Limit to top 10 as chain enforces this in finalize action as well
	if len(sns) > 10 {
		sns = sns[:10]
	}
	return sns, nil
}

func (t *BaseTask) orderByXORDistance(sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) <= 1 {
		return sns
	}
	seed := t.ActionID
	return orderSupernodesByDeterministicDistance(seed, sns)
}

// helper: get file size (bytes). returns 0 on error
func getFileSizeBytes(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (t *BaseTask) resourcesOK(ctx context.Context, client net.SupernodeClient, sn lumera.Supernode, minStorageBytes uint64, minFreeRamBytes uint64) bool {
	// In tests, skip resource thresholds (keep balance + health via nodeQualifies)
	if os.Getenv("INTEGRATION_TEST") == "true" {
		return true
	}
	status, err := client.GetSupernodeStatus(ctx)
	if err != nil || status == nil || status.Resources == nil {
		return false
	}
	// Storage: any volume must satisfy available >= minStorageBytes
	if minStorageBytes > 0 {
		ok := false
		for _, vol := range status.Resources.StorageVolumes {
			if vol != nil && vol.AvailableBytes >= minStorageBytes {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// RAM: available_gb must be >= required GiB
	if minFreeRamBytes > 0 {
		mem := status.Resources.Memory
		if mem == nil {
			return false
		}
		requiredGiB := float64(minFreeRamBytes) / (1024.0 * 1024.0 * 1024.0)
		if mem.AvailableGb < requiredGiB {
			return false
		}
	}
	return true
}

// filterByHealth returns nodes that report gRPC health SERVING.
func (t *BaseTask) filterByHealth(parent context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) == 0 {
		return sns
	}
	keep := make([]bool, len(sns))
	for i, sn := range sns {
		i, sn := i, sn
		ctx, cancel := context.WithTimeout(parent, connectionTimeout)
		func() {
			defer cancel()
			client, err := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, net.FactoryConfig{
				LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
				PeerType:           t.config.Account.PeerType,
			}).CreateClient(ctx, sn)
			if err != nil {
				return
			}
			defer client.Close(ctx)
			h, err := client.HealthCheck(ctx)
			if err == nil && h != nil && h.Status == grpc_health_v1.HealthCheckResponse_SERVING {
				keep[i] = true
			}
		}()
	}
	out := make(lumera.Supernodes, 0, len(sns))
	for i, sn := range sns {
		if keep[i] {
			out = append(out, sn)
		}
	}
	return out
}

// filterByMinBalance filters by requiring at least a minimum balance in the default fee denom.
func (t *BaseTask) filterByMinBalance(parent context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) == 0 {
		return sns
	}
	min := sdkmath.NewInt(1_000_000) // 1 LUME in ulume
	denom := txmod.DefaultFeeDenom
	keep := make([]bool, len(sns))
	for i, sn := range sns {
		i, sn := i, sn
		ctx, cancel := context.WithTimeout(parent, connectionTimeout)
		func() {
			defer cancel()
			bal, err := t.client.GetBalance(ctx, sn.CosmosAddress, denom)
			if err == nil && bal != nil && bal.Balance != nil && !bal.Balance.Amount.LT(min) {
				keep[i] = true
			}
		}()
	}
	out := make(lumera.Supernodes, 0, len(sns))
	for i, sn := range sns {
		if keep[i] {
			out = append(out, sn)
		}
	}
	return out
}
