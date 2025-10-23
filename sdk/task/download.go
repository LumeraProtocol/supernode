package task

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/sdk/net"
)

// timeouts
const (
	downloadTimeout = 15 * time.Minute
)

type CascadeDownloadTask struct {
	BaseTask
	actionId   string
	outputPath string
	signature  string
}

func NewCascadeDownloadTask(base BaseTask, actionId string, outputPath string, signature string) *CascadeDownloadTask {
	return &CascadeDownloadTask{
		BaseTask:   base,
		actionId:   actionId,
		outputPath: outputPath,
		signature:  signature,
	}
}

func (t *CascadeDownloadTask) Run(ctx context.Context) error {
	t.LogEvent(ctx, event.SDKTaskStarted, "Running cascade download task", nil)

	// 1 – fetch super-nodes (plain)
	supernodes, err := t.fetchSupernodes(ctx, t.Action.Height)
	if err != nil {
		t.LogEvent(ctx, event.SDKSupernodesUnavailable, "super-nodes unavailable", event.EventData{event.KeyError: err.Error()})
		t.LogEvent(ctx, event.SDKTaskFailed, "task failed", event.EventData{event.KeyError: err.Error()})
		return err
	}
	// Log available candidates; streaming will happen within download phase
	t.LogEvent(ctx, event.SDKSupernodesFound, "super-nodes fetched", event.EventData{event.KeyCount: len(supernodes)})

	// 2 – download from super-nodes
	if err := t.downloadFromSupernodes(ctx, supernodes); err != nil {
		t.LogEvent(ctx, event.SDKTaskFailed, "task failed", event.EventData{event.KeyError: err.Error()})
		return err
	}

	t.LogEvent(ctx, event.SDKTaskCompleted, "cascade download completed successfully", nil)
	return nil
}

func (t *CascadeDownloadTask) downloadFromSupernodes(ctx context.Context, supernodes lumera.Supernodes) error {
	factoryCfg := net.FactoryConfig{
		LocalCosmosAddress: t.config.Account.LocalCosmosAddress,
		PeerType:           t.config.Account.PeerType,
	}
	clientFactory := net.NewClientFactory(ctx, t.logger, t.keyring, t.client, factoryCfg)

	req := &supernodeservice.CascadeSupernodeDownloadRequest{
		ActionID:   t.actionId,
		TaskID:     t.TaskID,
		OutputPath: t.outputPath,
		Signature:  t.signature,
	}

	// Remove existing file once before starting attempts to allow overwrite
	if _, err := os.Stat(req.OutputPath); err == nil {
		if removeErr := os.Remove(req.OutputPath); removeErr != nil {
			return fmt.Errorf("failed to remove existing file %s: %w", req.OutputPath, removeErr)
		}
	}

	// Strict XOR-first qualification and attempts (downloads: storage-only threshold)
	ordered := t.orderByXORDistance(ctx, supernodes)

	var lastErr error
	attempted := 0
	for i, sn := range ordered {
		iteration := i + 1

		// Log download attempt
		t.LogEvent(ctx, event.SDKDownloadAttempt, "attempting download from super-node", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})

		// Ensure node qualifies before attempt
		if !t.nodeQualifies(ctx, sn, minStorageThresholdBytes, 0) {
			continue
		}

		attempted++
		if err := t.attemptDownload(ctx, sn, clientFactory, req); err != nil {
			// Log failure and continue with the rest
			t.LogEvent(ctx, event.SDKDownloadFailure, "download from super-node failed", event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        iteration,
				event.KeyError:            err.Error(),
			})
			lastErr = err
			continue
		}

		// Success; return to caller
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("failed to download from all super-nodes: %w", lastErr)
	}
	return fmt.Errorf("no supernodes available for download")
}

func (t *CascadeDownloadTask) attemptDownload(
	parent context.Context,
	sn lumera.Supernode,
	factory *net.ClientFactory,
	req *supernodeservice.CascadeSupernodeDownloadRequest,
) error {
	ctx, cancel := context.WithTimeout(parent, downloadTimeout)
	defer cancel()

	client, err := factory.CreateClient(ctx, sn)
	if err != nil {
		return fmt.Errorf("create client %s: %w", sn.CosmosAddress, err)
	}
	defer client.Close(ctx)

	req.EventLogger = func(ctx context.Context, evt event.EventType, msg string, data event.EventData) {
		t.LogEvent(ctx, evt, msg, data)
	}

	resp, err := client.Download(ctx, req)
	if err != nil {
		return fmt.Errorf("download from %s: %w", sn.CosmosAddress, err)
	}
	if !resp.Success {
		return fmt.Errorf("download rejected by %s: %s", sn.CosmosAddress, resp.Message)
	}

	return nil
}
