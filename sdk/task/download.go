package task

import (
	"context"
	"fmt"
	"time"

	"github.com/LumeraProtocol/supernode/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/sdk/adapters/supernodeservice"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/LumeraProtocol/supernode/sdk/net"
)

// timeouts
const (
	downloadTimeout = 5 * time.Minute
)

type CascadeDownloadTask struct {
	BaseTask
	actionId   string
	outputPath string
}

func NewCascadeDownloadTask(base BaseTask, actionId string, outputPath string) *CascadeDownloadTask {
	return &CascadeDownloadTask{
		BaseTask:   base,
		actionId:   actionId,
		outputPath: outputPath,
	}
}

func (t *CascadeDownloadTask) Run(ctx context.Context) error {
	t.LogEvent(ctx, event.SDKTaskStarted, "Running cascade download task", nil)

	// 1 – fetch super-nodes
	supernodes, err := t.fetchSupernodes(ctx, t.Action.Height)
	if err != nil {
		t.LogEvent(ctx, event.SDKSupernodesUnavailable, "super-nodes unavailable", event.EventData{event.KeyError: err.Error()})
		t.LogEvent(ctx, event.SDKTaskFailed, "task failed", event.EventData{event.KeyError: err.Error()})
		return err
	}
	t.LogEvent(ctx, event.SDKSupernodesFound, "super-nodes found", event.EventData{event.KeyCount: len(supernodes)})

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
	}

	var lastErr error
	for idx, sn := range supernodes {
		t.LogEvent(ctx, event.SDKDownloadAttempt, "attempting download from super-node", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        idx + 1,
		})

		if err := t.attemptDownload(ctx, sn, clientFactory, req); err != nil {
			lastErr = err
			t.LogEvent(ctx, event.SDKDownloadFailure, "download from super-node failed", event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        idx + 1,
				event.KeyError:            err.Error(),
			})
			continue
		}

		t.LogEvent(ctx, event.SDKDownloadSuccessful, "download successful", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        idx + 1,
		})
		return nil
	}
	return fmt.Errorf("failed to download from all super-nodes: %w", lastErr)
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

	t.LogEvent(ctx, event.SDKOutputPathReceived, "file downloaded", event.EventData{
		event.KeyOutputPath: resp.OutputPath,
		event.KeySupernode:  sn.CosmosAddress,
	})

	return nil
}
