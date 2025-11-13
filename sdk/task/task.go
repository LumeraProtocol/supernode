package task

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkmath "cosmossdk.io/math"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
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

const (
	prefilterParallelism    int   = 10
	minEligibleBalanceULUME int64 = 1_000_000 // 1 LUME in ulume
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

	// Log all fetched supernodes
	logtrace.Info(ctx, "Fetched supernodes from chain", logtrace.Fields{"total_count": len(sns), "height": height})
	for i, sn := range sns {
		endpoint := sn.GrpcEndpoint
		if endpoint == "" {
			endpoint = sn.CosmosAddress // fallback to address if endpoint not available
		}
		logtrace.Info(ctx, "Supernode from chain", logtrace.Fields{
			"index":          i + 1,
			"cosmos_address": sn.CosmosAddress,
			"endpoint":       endpoint,
			"state":          sn.State,
		})
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

// filterEligibleSupernodesParallel
// Fast, bounded-concurrency discovery that keeps only nodes that pass:
//
//	(1) gRPC Health SERVING
//	(2) Peers > 1 (via Status API; single-line gate for basic network liveness)
//	(3) On-chain balance >= 1 LUME (in ulume)
//
// Strategy:
//   - Spawn at most prefilterParallelism goroutines (bounded fan-out).
//   - For each node, run Health (incl. dial) and Balance concurrently under one timeout.
//   - Early-cancel sibling work on definitive failure to save time.
//   - Reuse healthy client connections during registration to skip a second dial.
func (t *BaseTask) filterEligibleSupernodesParallel(parent context.Context, sns lumera.Supernodes) (lumera.Supernodes, map[string]net.SupernodeClient) {
	if len(sns) == 0 {
		return sns, nil
	}

	// Step 0 — shared state for this pass
	keep := make([]bool, len(sns))
	var wg sync.WaitGroup
	sem := make(chan struct{}, prefilterParallelism)
	preClients := make(map[string]net.SupernodeClient)
	var mtx sync.Mutex

	// Step 0.1 — constants/resource handles used by probes
	min := sdkmath.NewInt(minEligibleBalanceULUME) // 1 LUME in ulume
	denom := txmod.DefaultFeeDenom

	factoryCfg := net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}
	clientFactory := net.NewClientFactory(parent, t.logger, t.keyring, t.client, factoryCfg)

	// Step 1 — spawn bounded goroutines, one per supernode
	for i, sn := range sns {
		wg.Add(1)
		go func(i int, sn lumera.Supernode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Step 1.1 — per-node shared timeout + cancellation for early exit
			probeCtx, cancel := context.WithTimeout(parent, connectionTimeout)
			defer cancel()

			var innerWg sync.WaitGroup
			var healthOK, balanceOK bool
			var c net.SupernodeClient

			innerWg.Add(2)

			// Step 1.2.1 — Health probe (dial + check) — create client here so balance starts immediately
			go func() {
				defer innerWg.Done()
				client, err := clientFactory.CreateClient(probeCtx, sn)
				if err != nil {
					// Unable to connect → ineligible; cancel sibling
					cancel()
					return
				}
				// Keep reference for potential reuse; close later based on outcome
				c = client
				// (1) Health SERVING gate
				h, err := client.HealthCheck(probeCtx)
				if err == nil && h != nil && h.Status == grpc_health_v1.HealthCheckResponse_SERVING {
					healthOK = true
				} else {
					// Health failed → ineligible; cancel sibling
					cancel()
					return
				}
				// (2) One-liner peers>1 gate using Status API to ensure network liveness
				if st, err := client.GetSupernodeStatus(probeCtx); err != nil || st == nil || st.Network == nil || st.Network.PeersCount <= 1 {
					healthOK = false
					cancel()
					return
				}
			}()

			// Step 1.2.2 — Balance probe (chain) — independent of gRPC dial
			go func() {
				defer innerWg.Done()
				bal, err := t.client.GetBalance(probeCtx, sn.CosmosAddress, denom)
				if err == nil && bal != nil && bal.Balance != nil && !bal.Balance.Amount.LT(min) {
					balanceOK = true
				} else {
					// Insufficient or error → ineligible; cancel sibling
					cancel()
				}
			}()

			// Step 1.3 — Wait for both probes, then decide eligibility and manage client lifecycle
			innerWg.Wait()
			if healthOK && balanceOK {
				keep[i] = true
				// Stash the client for reuse; key by CosmosAddress
				if c != nil {
					mtx.Lock()
					preClients[sn.CosmosAddress] = c
					mtx.Unlock()
				}
			} else {
				// Close any created client we won't reuse
				if c != nil {
					_ = c.Close(context.Background())
				}
			}
		}(i, sn)
	}
	wg.Wait()

	// Step 2 — build output preserving original order
	out := make(lumera.Supernodes, 0, len(sns))
	for i, sn := range sns {
		if keep[i] {
			out = append(out, sn)
		}
	}
	return out, preClients
}
