package task

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"sort"
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

	// Remove existing file once before starting attempts to allow overwrite
	if _, err := os.Stat(req.OutputPath); err == nil {
		if removeErr := os.Remove(req.OutputPath); removeErr != nil {
			return fmt.Errorf("failed to remove existing file %s: %w", req.OutputPath, removeErr)
		}
	}

	// Optionally rank supernodes by available memory to improve success for large files
	// We keep a short timeout per status fetch to avoid delaying downloads.
	type rankedSN struct {
		sn        lumera.Supernode
		availGB   float64
		hasStatus bool
	}
	ranked := make([]rankedSN, 0, len(supernodes))
	for _, sn := range supernodes {
		ranked = append(ranked, rankedSN{sn: sn})
	}

	// Probe supernode status with short timeouts and close clients promptly
	for i := range ranked {
		sn := ranked[i].sn
		// 2s status timeout to keep this pass fast
		stx, cancel := context.WithTimeout(ctx, 2*time.Second)
		client, err := clientFactory.CreateClient(stx, sn)
		if err != nil {
			cancel()
			continue
		}
		status, err := client.GetSupernodeStatus(stx)
		_ = client.Close(stx)
		cancel()
		if err != nil {
			continue
		}
		ranked[i].hasStatus = true
		ranked[i].availGB = status.Resources.Memory.AvailableGB
	}

	// Sort: nodes with status first, higher available memory first
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].hasStatus != ranked[j].hasStatus {
			return ranked[i].hasStatus && !ranked[j].hasStatus
		}
		return ranked[i].availGB > ranked[j].availGB
	})

	// Rebuild the supernodes list in the sorted order
	for i := range ranked {
		supernodes[i] = ranked[i].sn
	}

	// Try supernodes sequentially, one by one (now sorted)
	var lastErr error
	for idx, sn := range supernodes {
		iteration := idx + 1

		// Log download attempt
		t.LogEvent(ctx, event.SDKDownloadAttempt, "attempting download from super-node", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})

		if err := t.attemptDownload(ctx, sn, clientFactory, req); err != nil {
			// Log failure and continue to next supernode
			t.LogEvent(ctx, event.SDKDownloadFailure, "download from super-node failed", event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        iteration,
				event.KeyError:            err.Error(),
			})
			lastErr = err
			continue
		}

		// Success! Log and return
		t.LogEvent(ctx, event.SDKDownloadSuccessful, "download successful", event.EventData{
			event.KeySupernode:        sn.GrpcEndpoint,
			event.KeySupernodeAddress: sn.CosmosAddress,
			event.KeyIteration:        iteration,
		})
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

	// Emit connection established for observability (parity with StartCascade)
	t.LogEvent(ctx, event.SDKConnectionEstablished, "connection established", event.EventData{
		event.KeySupernode:        sn.GrpcEndpoint,
		event.KeySupernodeAddress: sn.CosmosAddress,
	})

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
		event.KeyOutputPath:       resp.OutputPath,
		event.KeySupernode:        sn.GrpcEndpoint,
		event.KeySupernodeAddress: sn.CosmosAddress,
	})

	return nil
}

// downloadResult holds the result of a successful download attempt
type downloadResult struct {
	SupernodeAddress  string
	SupernodeEndpoint string
	Iteration         int
	TempPath          string
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
	// Remove existing final file if it exists to allow overwrite (once per batch)
	finalOutputPath := req.OutputPath
	if _, err := os.Stat(finalOutputPath); err == nil {
		if removeErr := os.Remove(finalOutputPath); removeErr != nil {
			return nil, []error{fmt.Errorf("failed to remove existing file %s: %w", finalOutputPath, removeErr)}
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

	// Track per-attempt temporary output paths for safe concurrent writes
	tmpPaths := make([]string, len(batch))

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
				ActionID: req.ActionID,
				TaskID:   req.TaskID,
				// Use a unique temporary path per attempt to avoid concurrent writes
				OutputPath: filepath.Join(filepath.Dir(finalOutputPath), fmt.Sprintf(".%s.part.%d", filepath.Base(finalOutputPath), idx)),
				Signature:  req.Signature,
			}
			tmpPaths[idx] = reqCopy.OutputPath

			err := t.attemptDownload(batchCtx, sn, factory, reqCopy)
			if err != nil {
				// Best-effort cleanup of the partial file for this attempt
				_ = os.Remove(reqCopy.OutputPath)
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
					TempPath:          reqCopy.OutputPath,
				},
				idx: idx,
			}
		}(sn, idx, iteration)
	}

	// Collect results
	var errs []error
	for i := 0; i < len(batch); i++ {
		select {
		case result := <-resultCh:
			if result.success != nil {
				// Attempt to move the temp file to the final destination atomically
				if err := os.Rename(result.success.TempPath, finalOutputPath); err != nil {
					// Treat rename failure as a batch error
					cancelBatch()
					// Drain remaining results to avoid goroutine leaks
					go func() {
						for j := i + 1; j < len(batch); j++ {
							<-resultCh
						}
					}()
					return nil, []error{fmt.Errorf("finalize download (rename) failed: %w", err)}
				}

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
			// Classify failure reason when possible
			data := event.EventData{
				event.KeySupernode:        sn.GrpcEndpoint,
				event.KeySupernodeAddress: sn.CosmosAddress,
				event.KeyIteration:        baseIteration + result.idx + 1,
				event.KeyError:            result.err.Error(),
			}
			msg := "download from super-node failed"
			if stderrors.Is(result.err, context.DeadlineExceeded) {
				data[event.KeyMessage] = "timeout"
				msg += " | reason=timeout"
			} else if stderrors.Is(result.err, context.Canceled) {
				data[event.KeyMessage] = "canceled"
				msg += " | reason=canceled"
			}
			t.LogEvent(ctx, event.SDKDownloadFailure, msg, data)
			errs = append(errs, result.err)

		case <-ctx.Done():
			return nil, []error{ctx.Err()}
		}
	}

	// All attempts in this batch failed
	return nil, errs
}
