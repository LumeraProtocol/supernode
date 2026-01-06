package task

import (
	"context"
	"errors"
	"fmt"
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
func (t *BaseTask) filterEligibleSupernodesParallel(parent context.Context, sns lumera.Supernodes) (lumera.Supernodes, map[string]net.SupernodeClient, error) {
	if len(sns) == 0 {
		return sns, nil, nil
	}

	// Step 0 — shared state for this pass
	keep := make([]bool, len(sns))
	rejectionReasons := make([]string, len(sns))
	var wg sync.WaitGroup
	sem := make(chan struct{}, prefilterParallelism)
	preClients := make(map[string]net.SupernodeClient)
	var mtx sync.Mutex

	// Step 0.1 — constants/resource handles used by probes
	min := sdkmath.NewInt(minEligibleBalanceULUME) // 1 LUME in ulume
	denom := txmod.DefaultFeeDenom

	factoryCfg := net.FactoryConfig{
		KeyName:  t.config.Account.KeyName,
		PeerType: t.config.Account.PeerType,
	}
	clientFactory, err := net.NewClientFactory(parent, t.logger, t.keyring, t.client, factoryCfg)
	if err != nil {
		t.LogEvent(parent, event.SDKTaskFailed, "Failed to create client factory", event.EventData{event.KeyError: err.Error()})
		return nil, nil, fmt.Errorf("failed to create client factory: %w", err)
	}

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

			type healthResult struct {
				ok     bool
				client net.SupernodeClient
				reason string
			}
			type balanceResult struct {
				ok     bool
				reason string
			}

			healthCh := make(chan healthResult, 1)
			balanceCh := make(chan balanceResult, 1)

			// Step 1.2.1 — Health probe (dial + check)
			go func() {
				res := healthResult{ok: false}
				defer func() { healthCh <- res }()

				client, err := clientFactory.CreateClient(probeCtx, sn)
				if err != nil {
					// Unable to connect → ineligible; cancel sibling
					res.reason = fmt.Sprintf("connection failed: %v", err)
					cancel()
					return
				}

				// (1) Health SERVING gate
				h, err := client.HealthCheck(probeCtx)
				if err != nil || h == nil || h.Status != grpc_health_v1.HealthCheckResponse_SERVING {
					if err != nil {
						res.reason = fmt.Sprintf("health check failed: %v", err)
					} else if h == nil {
						res.reason = "health check returned nil"
					} else {
						res.reason = fmt.Sprintf("health status: %v (expected SERVING)", h.Status)
					}
					_ = client.Close(context.Background())
					cancel()
					return
				}

				// (2) One-liner peers>1 gate using Status API to ensure network liveness
				st, err := client.GetSupernodeStatus(probeCtx)
				if err != nil || st == nil || st.Network == nil || st.Network.PeersCount <= 1 {
					if err != nil {
						res.reason = fmt.Sprintf("status check failed: %v", err)
					} else if st == nil || st.Network == nil {
						res.reason = "status or network info unavailable"
					} else {
						res.reason = fmt.Sprintf("insufficient peers: %d (need > 1)", st.Network.PeersCount)
					}
					_ = client.Close(context.Background())
					cancel()
					return
				}

				res.ok = true
				res.client = client
			}()

			// Step 1.2.2 — Balance probe (chain) — independent of gRPC dial
			go func() {
				res := balanceResult{ok: false}
				defer func() { balanceCh <- res }()

				bal, err := t.client.GetBalance(probeCtx, sn.CosmosAddress, denom)
				if err == nil && bal != nil && bal.Balance != nil && !bal.Balance.Amount.LT(min) {
					res.ok = true
					return
				}

				// Insufficient or error → ineligible; cancel sibling
				if err != nil {
					res.reason = fmt.Sprintf("balance check failed: %v", err)
				} else if bal == nil || bal.Balance == nil {
					res.reason = "balance info unavailable"
				} else {
					actualLUME := bal.Balance.Amount.Quo(sdkmath.NewInt(1_000_000))
					res.reason = fmt.Sprintf("insufficient balance: %s LUME (need >= 1 LUME)", actualLUME.String())
				}
				cancel()
			}()

			// Step 1.3 — Wait for both probes, then decide eligibility and manage client lifecycle
			hr := <-healthCh
			br := <-balanceCh

			if hr.ok && br.ok {
				keep[i] = true
				if hr.client != nil {
					// Stash the client for reuse; key by CosmosAddress
					mtx.Lock()
					preClients[sn.CosmosAddress] = hr.client
					mtx.Unlock()
				}
				return
			}

			// Close any created client we won't reuse
			if hr.client != nil {
				_ = hr.client.Close(context.Background())
			}

			if hr.reason != "" {
				rejectionReasons[i] = hr.reason
			} else {
				rejectionReasons[i] = br.reason
			}
		}(i, sn)
	}
	wg.Wait()

	// Step 2 — log eligibility results
	t.logger.Info(parent, "Supernode eligibility check started")
	acceptedCount := 0
	rejectedCount := 0
	for i, sn := range sns {
		endpoint := sn.GrpcEndpoint
		if endpoint == "" {
			endpoint = sn.CosmosAddress // fallback to address if endpoint not available
		}

		if keep[i] {
			t.logger.Debug(parent, "Supernode accepted", "endpoint", endpoint)
			acceptedCount++
		} else {
			reason := rejectionReasons[i]
			if reason == "" {
				reason = "unknown reason"
			}
			t.logger.Debug(parent, "Supernode rejected", "endpoint", endpoint, "reason", reason)
			rejectedCount++
		}
	}
	t.logger.Info(parent, "Supernode eligibility check completed",
		"accepted", acceptedCount,
		"rejected", rejectedCount,
		"total", len(sns))
	if acceptedCount == 0 {
		reasonCounts := make(map[string]int)
		for i, sn := range sns {
			if keep[i] {
				continue
			}
			reason := rejectionReasons[i]
			if reason == "" {
				reason = "unknown reason"
			}
			reasonCounts[reason]++

			endpoint := sn.GrpcEndpoint
			if endpoint == "" {
				endpoint = sn.CosmosAddress
			}
			t.logger.Info(parent, "Supernode rejected (eligible=0)", "endpoint", endpoint, "reason", reason)
		}
		t.logger.Info(parent, "Supernode rejection summary", "reasons", reasonCounts)
	}

	// Step 3 — build output preserving original order
	out := make(lumera.Supernodes, 0, len(sns))
	for i, sn := range sns {
		if keep[i] {
			out = append(out, sn)
		}
	}
	return out, preClients, nil
}
