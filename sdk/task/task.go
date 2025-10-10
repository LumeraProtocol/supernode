package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	sdkmath "cosmossdk.io/math"
	"github.com/LumeraProtocol/supernode/v2/pkg/errgroup"
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

// (removed) fetchSupernodes: replaced by fetchSupernodesWithLoads single-pass probe

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

	// Then check P2P peers count via status
	status, err := client.GetSupernodeStatus(ctx)
	if err != nil {
		t.logger.Info(ctx, "reject supernode: status fetch failed", "error", err)
		return false
	}
	if status.Network.PeersCount <= 1 {
		t.logger.Info(ctx, "reject supernode: insufficient peers", "peers_count", status.Network.PeersCount)
		return false
	}

	denom := txmod.DefaultFeeDenom // base denom (micro), e.g., "ulume"
	bal, err := t.client.GetBalance(ctx, sn.CosmosAddress, denom)
	if err != nil || bal == nil || bal.Balance == nil {
		t.logger.Info(ctx, "reject supernode: balance fetch failed or empty", "error", err)
		return false
	}
	// Require at least 1 LUME = 10^6 micro (ulume)
	min := sdkmath.NewInt(1_000_000)
	if bal.Balance.Amount.LT(min) {
		t.logger.Info(ctx, "reject supernode: insufficient balance", "amount", bal.Balance.Amount.String(), "min", min.String())
		return false
	}

	return true
}

// fetchSupernodesWithLoads performs a single-pass probe that both sanitizes candidates
// and captures their current running task load for initial ranking.
// Returns the healthy supernodes and a map of node-key -> load.
func (t *BaseTask) fetchSupernodesWithLoads(ctx context.Context, height int64) (lumera.Supernodes, map[string]int, error) {
	sns, err := t.client.GetSupernodes(ctx, height)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch supernodes: %w", err)
	}
	if len(sns) == 0 {
		return nil, nil, errors.New("no supernodes found")
	}

	healthy := make(lumera.Supernodes, 0, len(sns))
	loads := make(map[string]int, len(sns))
	mu := sync.Mutex{}

	eg, ctx := errgroup.WithContext(ctx)
	for _, sn := range sns {
		sn := sn
		eg.Go(func() error {
			cctx, cancel := context.WithTimeout(ctx, connectionTimeout)
			defer cancel()

			client, err := net.NewClientFactory(cctx, t.logger, t.keyring, t.client, net.FactoryConfig{
				LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
				PeerType:           t.config.Account.PeerType,
			}).CreateClient(cctx, sn)
			if err != nil {
				t.logger.Info(cctx, "reject supernode: client create failed", "reason", err.Error(), "endpoint", sn.GrpcEndpoint, "cosmos", sn.CosmosAddress)
				return nil
			}
			defer client.Close(cctx)

			// Health
			resp, err := client.HealthCheck(cctx)
			if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
				statusStr := "nil"
				if resp != nil {
					statusStr = resp.Status.String()
				}
				t.logger.Info(cctx, "reject supernode: health not SERVING", "error", err, "status", statusStr)
				return nil
			}

			// Status (for peers + load)
			status, err := client.GetSupernodeStatus(cctx)
			if err != nil {
				t.logger.Info(cctx, "reject supernode: status fetch failed", "error", err)
				return nil
			}
			if status.Network.PeersCount <= 1 {
				t.logger.Info(cctx, "reject supernode: insufficient peers", "peers_count", status.Network.PeersCount)
				return nil
			}

			// Compute load from running tasks (sum of task_count across services)
			total := 0
			for _, st := range status.GetRunningTasks() {
				if st == nil {
					continue
				}
				if c := int(st.GetTaskCount()); c > 0 {
					total += c
				} else if ids := st.GetTaskIds(); len(ids) > 0 {
					total += len(ids)
				}
			}

			// Balance
			denom := txmod.DefaultFeeDenom
			bal, err := t.client.GetBalance(cctx, sn.CosmosAddress, denom)
			if err != nil || bal == nil || bal.Balance == nil {
				t.logger.Info(cctx, "reject supernode: balance fetch failed or empty", "error", err)
				return nil
			}
			min := sdkmath.NewInt(1_000_000)
			if bal.Balance.Amount.LT(min) {
				t.logger.Info(cctx, "reject supernode: insufficient balance", "amount", bal.Balance.Amount.String(), "min", min.String())
				return nil
			}

			// Accept
			mu.Lock()
			healthy = append(healthy, sn)
			key := sn.CosmosAddress
			if key == "" {
				key = sn.GrpcEndpoint
			}
			loads[key] = total
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, fmt.Errorf("health-check goroutines: %w", err)
	}
	if len(healthy) == 0 {
		return nil, nil, errors.New("no healthy supernodes found")
	}
	return healthy, loads, nil
}

// orderByLoadSnapshotThenDeterministic sorts using a provided load snapshot; nodes missing
// in the snapshot are considered unknown-load and placed after known-load nodes.
func (t *BaseTask) orderByLoadSnapshotThenDeterministic(sns lumera.Supernodes, loads map[string]int) lumera.Supernodes {
	if len(sns) <= 1 {
		return sns
	}

	det := orderSupernodesByDeterministicDistance(t.ActionID, append(lumera.Supernodes(nil), sns...))
	idx := make(map[string]int, len(det))
	for i, sn := range det {
		key := sn.CosmosAddress
		if key == "" {
			key = sn.GrpcEndpoint
		}
		idx[key] = i
	}

	type scored struct {
		sn        lumera.Supernode
		load      int
		loadKnown bool
		tieIdx    int
	}
	arr := make([]scored, 0, len(sns))
	for _, sn := range sns {
		key := sn.CosmosAddress
		if key == "" {
			key = sn.GrpcEndpoint
		}
		l, ok := loads[key]
		arr = append(arr, scored{sn: sn, load: l, loadKnown: ok, tieIdx: idx[key]})
	}

	sort.Slice(arr, func(i, j int) bool {
		ai, aj := arr[i], arr[j]
		if ai.loadKnown != aj.loadKnown {
			return ai.loadKnown
		}
		if ai.loadKnown && aj.loadKnown && ai.load != aj.load {
			return ai.load < aj.load
		}
		return ai.tieIdx < aj.tieIdx
	})

	out := make(lumera.Supernodes, len(arr))
	for i := range arr {
		out[i] = arr[i].sn
	}
	return out
}

// orderByLoadThenDeterministic ranks supernodes by their current running task count (ascending).
// Ties are broken deterministically using orderSupernodesByDeterministicDistance with ActionID as seed.
func (t *BaseTask) orderByLoadThenDeterministic(parent context.Context, sns lumera.Supernodes) lumera.Supernodes {
	if len(sns) <= 1 {
		return sns
	}

	// Precompute deterministic tie-break order index per node
	det := orderSupernodesByDeterministicDistance(t.ActionID, append(lumera.Supernodes(nil), sns...))
	idx := make(map[string]int, len(det))
	for i, sn := range det {
		key := sn.CosmosAddress
		if key == "" {
			key = sn.GrpcEndpoint
		}
		idx[key] = i
	}

	type scored struct {
		sn        lumera.Supernode
		load      int
		loadKnown bool
		tieIdx    int
	}

	out := make([]scored, len(sns))

	// Collect loads in parallel under the same short connection timeout.
	eg, ctx := errgroup.WithContext(parent)
	for i, sn := range sns {
		i, sn := i, sn
		out[i] = scored{sn: sn, load: 0, loadKnown: false, tieIdx: func() int {
			k := sn.CosmosAddress
			if k == "" {
				k = sn.GrpcEndpoint
			}
			return idx[k]
		}()}
		eg.Go(func() error {
			cctx, cancel := context.WithTimeout(ctx, connectionTimeout)
			defer cancel()
			client, err := net.NewClientFactory(cctx, t.logger, t.keyring, t.client, net.FactoryConfig{
				LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
				PeerType:           t.config.Account.PeerType,
			}).CreateClient(cctx, sn)
			if err != nil {
				return nil // unknown load; keep candidate
			}
			defer client.Close(cctx)
			status, err := client.GetSupernodeStatus(cctx)
			if err != nil || status == nil {
				return nil
			}
			// Sum total running tasks across services
			total := 0
			for _, st := range status.GetRunningTasks() {
				if st == nil {
					continue
				}
				if c := int(st.GetTaskCount()); c > 0 {
					total += c
				} else if ids := st.GetTaskIds(); len(ids) > 0 {
					total += len(ids)
				}
			}
			out[i].load = total
			out[i].loadKnown = true
			return nil
		})
	}
	_ = eg.Wait() // best-effort; unknown loads are placed after known ones below

	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i], out[j]
		if ai.loadKnown != aj.loadKnown {
			return ai.loadKnown // known loads first
		}
		if ai.loadKnown && aj.loadKnown && ai.load != aj.load {
			return ai.load < aj.load
		}
		// Tie-break deterministically
		return ai.tieIdx < aj.tieIdx
	})

	res := make(lumera.Supernodes, len(out))
	for i := range out {
		res[i] = out[i].sn
	}
	return res
}
