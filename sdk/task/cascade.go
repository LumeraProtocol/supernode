package task

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/sdk/net"
)

// connectionTimeout is defined in timeouts.go for the task package.

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
	t.LogEvent(ctx, event.SDKTaskStarted, "Running cascade task", nil)

	// Validate file size before proceeding
	if err := ValidateFileSize(t.filePath); err != nil {
		t.LogEvent(ctx, event.SDKTaskFailed, "File validation failed", event.EventData{event.KeyError: err.Error()})
		return err
	}

	// 1 - Fetch the supernodes
	supernodes, err := t.fetchSupernodes(ctx, t.Action.Height)

	if err != nil {
		t.LogEvent(ctx, event.SDKSupernodesUnavailable, "Supernodes unavailable", event.EventData{event.KeyError: err.Error()})
		t.LogEvent(ctx, event.SDKTaskFailed, "Task failed", event.EventData{event.KeyError: err.Error()})
		return err
	}

	// 2 - Pre-filter: balance -> health -> XOR rank -> resources, then hand over
	originalCount := len(supernodes)
	supernodes = t.filterByMinBalance(ctx, supernodes)
	supernodes = t.filterByHealth(ctx, supernodes)
	supernodes = t.orderByXORDistance(supernodes)
	t.LogEvent(ctx, event.SDKSupernodesFound, "Supernodes filtered", event.EventData{event.KeyTotal: originalCount, event.KeyCount: len(supernodes)})

	// 2 - Register with the supernodes
	if err := t.registerWithSupernodes(ctx, supernodes); err != nil {
		t.LogEvent(ctx, event.SDKTaskFailed, "Task failed", event.EventData{event.KeyError: err.Error()})
		return err
	}

	t.LogEvent(ctx, event.SDKTaskCompleted, "Cascade task completed successfully", nil)

	return nil
}

func (t *CascadeTask) registerWithSupernodes(ctx context.Context, supernodes lumera.Supernodes) error {
	factoryCfg := net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}
	clientFactory := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, factoryCfg)

	req := &supernodeservice.CascadeSupernodeRegisterRequest{
		FilePath: t.filePath,
		ActionID: t.ActionID,
		TaskId:   t.TaskID,
	}

	ordered := supernodes

	var lastErr error
	attempted := 0
	for i, sn := range ordered {
		iteration := i + 1

		t.LogEvent(ctx, event.SDKRegistrationAttempt, "attempting registration with supernode", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})

		attempted++
		if err := t.attemptRegistration(ctx, iteration-1, sn, clientFactory, req); err != nil {
			t.LogEvent(ctx, event.SDKRegistrationFailure, "registration with supernode failed", event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        iteration,
				event.KeyError:            err.Error(),
			})
			lastErr = err
			continue
		}

		t.LogEvent(ctx, event.SDKRegistrationSuccessful, "successfully registered with supernode", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})
		return nil // success
	}
	if attempted == 0 {
		return fmt.Errorf("no eligible supernodes to register")
	}
	if lastErr != nil {
		return fmt.Errorf("failed to upload to all supernodes: %w", lastErr)
	}
	return fmt.Errorf("failed to upload to all supernodes")
}

func (t *CascadeTask) attemptRegistration(ctx context.Context, _ int, sn lumera.Supernode, factory *net.ClientFactory, req *supernodeservice.CascadeSupernodeRegisterRequest) error {
	client, err := factory.CreateClient(ctx, sn)
	if err != nil {
		return fmt.Errorf("create client %s: %w", sn.CosmosAddress, err)
	}
	defer client.Close(ctx)

	// Emit connection established event for observability
	t.LogEvent(ctx, event.SDKConnectionEstablished, "Connection to supernode established", event.EventData{
		event.KeySupernode:        sn.GrpcEndpoint,
		event.KeySupernodeAddress: sn.CosmosAddress,
	})

	// Just-in-time resource check for uploads (storage + RAM >= 8x file size)
	var minRam uint64
	if size := getFileSizeBytes(t.filePath); size > 0 {
		minRam = uint64(size) * uploadRAMMultiplier
	}
	if ok := t.resourcesOK(ctx, client, sn, minStorageThresholdBytes, minRam); !ok {
		return fmt.Errorf("resource check failed")
	}

	req.EventLogger = func(ctx context.Context, evt event.EventType, msg string, data event.EventData) {
		t.LogEvent(ctx, evt, msg, data)
	}
	// Use ctx directly; per-phase timers are applied inside the adapter
	resp, err := client.RegisterCascade(ctx, req)
	if err != nil {
		return fmt.Errorf("upload to %s: %w", sn.CosmosAddress, err)
	}
	if !resp.Success {
		return fmt.Errorf("upload rejected by %s: %s", sn.CosmosAddress, resp.Message)
	}

	t.LogEvent(ctx, event.SDKTaskTxHashReceived, "txhash received", event.EventData{
		event.KeyTxHash:    resp.TxHash,
		event.KeySupernode: sn.CosmosAddress,
	})

	return nil
}
