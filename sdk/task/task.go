package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

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

// isServing pings the super-node once with a short timeout.
func (t *BaseTask) isServing(parent context.Context, sn lumera.Supernode) bool {
	ctx, cancel := context.WithTimeout(parent, connectionTimeout)
	defer cancel()

	client, err := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}).CreateClient(ctx, sn)
	if err != nil {
		t.logger.Info(ctx, "reject supernode: client create failed", "reason", err.Error(), "endpoint", sn.GrpcEndpoint, "cosmos", sn.CosmosAddress)
		return false
	}
	defer client.Close(ctx)

	// First check gRPC health
	resp, err := client.HealthCheck(ctx)
	if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		statusStr := "nil"
		if resp != nil {
			statusStr = resp.Status.String()
		}
		t.logger.Info(ctx, "reject supernode: health not SERVING", "error", err, "status", statusStr)
		return false
	}
	return true
}

// No health, status, balance or load checks are done here.
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

func (t *BaseTask) orderByFreeRAM(parent context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) <= 1 {
		return sns
	}

	type scored struct {
		idx   int
		sn    lumera.Supernode
		ramGb float64
		known bool
	}

	out := make([]scored, len(sns))
	// Best-effort parallel status fetch; do not filter or fail.
	// We intentionally avoid health/peer/balance checks here.
	for i, sn := range sns {
		out[i] = scored{idx: i, sn: sn, ramGb: 0, known: false}
	}

	// Query in parallel with a short timeout to avoid blocking too long per node
	// Reuse the connectionTimeout constant for symmetry with health probes.
	type result struct {
		i   int
		ram float64
		ok  bool
	}
	ch := make(chan result, len(sns))
	for i, sn := range sns {
		i, sn := i, sn
		go func() {
			cctx, cancel := context.WithTimeout(parent, connectionTimeout)
			defer cancel()
			client, err := net.NewClientFactory(cctx, t.logger, t.keyring, t.client, net.FactoryConfig{
				LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
				PeerType:           t.config.Account.PeerType,
			}).CreateClient(cctx, sn)
			if err != nil {
				ch <- result{i: i, ram: 0, ok: false}
				return
			}
			defer client.Close(cctx)

			status, err := client.GetSupernodeStatus(cctx)
			if err != nil || status == nil {
				ch <- result{i: i, ram: 0, ok: false}
				return
			}
			res := status.GetResources()
			if res == nil || res.GetMemory() == nil {
				ch <- result{i: i, ram: 0, ok: false}
				return
			}
			ch <- result{i: i, ram: res.GetMemory().GetAvailableGb(), ok: true}
		}()
	}
	// Collect results with a cap bounded by len(sns)
	for k := 0; k < len(sns); k++ {
		r := <-ch
		if r.ok {
			out[r.i].ramGb = r.ram
			out[r.i].known = true
		}
	}

	// Known RAM first, then by RAM desc. For ties and unknowns, preserve original order.
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i], out[j]
		if ai.known != aj.known {
			return ai.known
		}
		if ai.known && aj.known && ai.ramGb != aj.ramGb {
			return ai.ramGb > aj.ramGb
		}
		return ai.idx < aj.idx
	})

	res := make(lumera.Supernodes, len(out))
	for i := range out {
		res[i] = out[i].sn
	}
	return res
}

// filterByMinBalance filters supernodes by requiring at least a minimum balance
// in the default fee denom. This runs concurrently and is intended to be used
// once during initial discovery only.
func (t *BaseTask) filterByMinBalance(parent context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) == 0 {
		return sns
	}
	// Require at least 1 LUME = 10^6 ulume by default.
	min := sdkmath.NewInt(1_000_000)
	denom := txmod.DefaultFeeDenom

	keep := make([]bool, len(sns))
	var wg sync.WaitGroup
	wg.Add(len(sns))
	for i, sn := range sns {
		i, sn := i, sn
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(parent, connectionTimeout)
			defer cancel()
			bal, err := t.client.GetBalance(ctx, sn.CosmosAddress, denom)
			if err != nil || bal == nil || bal.Balance == nil {
				t.logger.Info(ctx, "reject supernode: balance fetch failed or empty", "error", err, "address", sn.CosmosAddress)
				return
			}
			if bal.Balance.Amount.LT(min) {
				t.logger.Info(ctx, "reject supernode: insufficient balance", "amount", bal.Balance.Amount.String(), "min", min.String(), "address", sn.CosmosAddress)
				return
			}
			keep[i] = true
		}()
	}
	wg.Wait()

	out := make(lumera.Supernodes, 0, len(sns))
	for i, sn := range sns {
		if keep[i] {
			out = append(out, sn)
		}
	}
	return out
}
