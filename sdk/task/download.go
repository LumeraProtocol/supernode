package task

import (
	"context"
	"fmt"
	"os"
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
		Signature:  t.signature,
	}

	// Process supernodes in pairs
	var allErrors []error
	for i := 0; i < len(supernodes); i += 2 {
		// Determine how many supernodes to try in this batch (1 or 2)
		batchSize := 2
		if i+1 >= len(supernodes) {
			batchSize = 1
		}

		t.logger.Info(ctx, "attempting concurrent download from supernode batch", "batch_start", i, "batch_size", batchSize)

		// Try downloading from this batch concurrently
		result, batchErrors := t.attemptConcurrentDownload(ctx, supernodes[i:i+batchSize], clientFactory, req, i)

		if result != nil {
			// Success! Log and return
			t.LogEvent(ctx, event.SDKDownloadSuccessful, "download successful", event.EventData{
				event.KeySupernode:        result.SupernodeEndpoint,
				event.KeySupernodeAddress: result.SupernodeAddress,
				event.KeyIteration:        result.Iteration,
			})
			return nil
		}

		// Both (or the single one) failed, collect errors
		allErrors = append(allErrors, batchErrors...)

		// Log batch failure
		t.logger.Warn(ctx, "download batch failed", "batch_start", i, "batch_size", batchSize, "errors", len(batchErrors))
	}

	// All attempts failed
	if len(allErrors) > 0 {
		return fmt.Errorf("failed to download from all super-nodes: %v", allErrors)
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

	t.LogEvent(ctx, event.SDKOutputPathReceived, "file downloaded", event.EventData{
		event.KeyOutputPath: resp.OutputPath,
		event.KeySupernode:  sn.CosmosAddress,
	})

	return nil
}

// downloadResult holds the result of a successful download attempt
type downloadResult struct {
	SupernodeAddress  string
	SupernodeEndpoint string
	Iteration         int
}

// attemptConcurrentDownload tries to download from multiple supernodes concurrently
// Returns the first successful result or all errors if all attempts fail
func (t *CascadeDownloadTask) attemptConcurrentDownload(
	ctx context.Context,
	batch lumera.Supernodes,
	factory *net.ClientFactory,
	req *supernodeservice.CascadeSupernodeDownloadRequest,
	baseIteration int,
) (*downloadResult, []error) {
	// Remove existing file if it exists to allow overwrite (do this once before concurrent attempts)
	if _, err := os.Stat(req.OutputPath); err == nil {
		if removeErr := os.Remove(req.OutputPath); removeErr != nil {
			return nil, []error{fmt.Errorf("failed to remove existing file %s: %w", req.OutputPath, removeErr)}
		}
	}

	// Create a cancellable context for this batch
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	// Channels for results
	type attemptResult struct {
		success *downloadResult
		err     error
		idx     int
	}
	resultCh := make(chan attemptResult, len(batch))

	// Start concurrent download attempts
	for idx, sn := range batch {
		iteration := baseIteration + idx + 1

		// Log download attempt
		t.LogEvent(ctx, event.SDKDownloadAttempt, "attempting download from super-node", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})

		go func(sn lumera.Supernode, idx int, iter int) {
			// Create a copy of the request for this goroutine
			reqCopy := &supernodeservice.CascadeSupernodeDownloadRequest{
				ActionID:   req.ActionID,
				TaskID:     req.TaskID,
				OutputPath: req.OutputPath,
				Signature:  req.Signature,
			}

			err := t.attemptDownload(batchCtx, sn, factory, reqCopy)
			if err != nil {
				resultCh <- attemptResult{
					err: err,
					idx: idx,
				}
				return
			}

			resultCh <- attemptResult{
				success: &downloadResult{
					SupernodeAddress:  sn.CosmosAddress,
					SupernodeEndpoint: sn.GrpcEndpoint,
					Iteration:         iter,
				},
				idx: idx,
			}
		}(sn, idx, iteration)
	}

	// Collect results
	var errors []error
	for i := range len(batch) {
		select {
		case result := <-resultCh:
			if result.success != nil {
				// Success! Cancel other attempts and return
				cancelBatch()
				// Drain remaining results to avoid goroutine leaks
				go func() {
					for j := i + 1; j < len(batch); j++ {
						<-resultCh
					}
				}()
				return result.success, nil
			}

			// Log failure
			sn := batch[result.idx]
			t.LogEvent(ctx, event.SDKDownloadFailure, "download from super-node failed", event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        baseIteration + result.idx + 1,
				event.KeyError:            result.err.Error(),
			})
			errors = append(errors, result.err)

		case <-ctx.Done():
			return nil, []error{ctx.Err()}
		}
	}

	// All attempts in this batch failed
	return nil, errors
}
