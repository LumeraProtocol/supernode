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
	return sns, nil
}

// orderByXORDistance ranks supernodes by XOR distance to the action's data hash.
// If decoding metadata fails, falls back to using the action ID as the seed.
func (t *BaseTask) orderByXORDistance(ctx context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) <= 1 {
		return sns
	}
	// Try to decode the action metadata to get the Cascade data hash as seed
	seed := t.ActionID
	if t.client != nil && (t.Action.Metadata != nil || t.Action.ActionType != "") {
		if meta, err := t.client.DecodeCascadeMetadata(ctx, t.Action); err == nil && meta.DataHash != "" {
			seed = meta.DataHash
		}
	}
	return orderSupernodesByDeterministicDistance(seed, sns)
}

// filterByResourceThresholds removes supernodes that do not satisfy minimum
// available storage and free RAM thresholds.
// - minStorageBytes: minimum available storage on any volume (bytes)
// - minFreeRamBytes: minimum free RAM (bytes). If 0, RAM check is skipped.

// helper: get file size (bytes). returns 0 on error
func getFileSizeBytes(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// nodeQualifies performs balance, health, and resource checks for a supernode.
func (t *BaseTask) nodeQualifies(parent context.Context, sn lumera.Supernode, minStorageBytes uint64, minFreeRamBytes uint64) bool {
	// 1) Balance check (require at least 1 LUME)
	if !t.balanceOK(parent, sn) {
		return false
	}

	// 2) Health + resources via a single client session
	ctx, cancel := context.WithTimeout(parent, connectionTimeout)
	defer cancel()
	client, err := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}).CreateClient(ctx, sn)
	if err != nil {
		return false
	}
	defer client.Close(ctx)

	// Health check
	h, err := client.HealthCheck(ctx)
	if err != nil || h == nil || h.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return false
	}

	// Resource thresholds
	return t.resourcesOK(ctx, client, sn, minStorageBytes, minFreeRamBytes)
}

func (t *BaseTask) balanceOK(parent context.Context, sn lumera.Supernode) bool {
	ctx, cancel := context.WithTimeout(parent, connectionTimeout)
	defer cancel()
	min := sdkmath.NewInt(1_000_000) // 1 LUME in ulume
	denom := txmod.DefaultFeeDenom
	bal, err := t.client.GetBalance(ctx, sn.CosmosAddress, denom)
	if err != nil || bal == nil || bal.Balance == nil {
		return false
	}
	if bal.Balance.Amount.LT(min) {
		return false
	}
	return true
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
