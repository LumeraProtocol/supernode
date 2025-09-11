package adaptors

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/storage"
	"github.com/pkg/errors"
)

const (
	loadSymbolsBatchSize = 2500
	// Minimum first-pass coverage to store before returning from Register (percent)
	storeSymbolsPercent = 18
)

// P2PService defines the interface for storing data in the P2P layer.
//
//go:generate mockgen -destination=mocks/p2p_mock.go -package=cascadeadaptormocks -source=p2p.go
type P2PService interface {
	// StoreArtefacts stores ID files and RaptorQ symbols.
	//
	// Aggregation model:
	// - Each underlying StoreBatch returns (ratePct, requests) where requests is
	//   the number of node RPCs. The aggregated success rate can be computed as
	//   a weighted average by requests across metadata and symbol batches,
	//   yielding a global success view across all node calls attempted for this action.
	//   See implementation notes for item‑weighted aggregation currently in use.
	//
	// Returns detailed metrics for both categories along with an aggregated view.
	StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) (StoreArtefactsMetrics, error)
}

// p2pImpl is the default implementation of the P2PService interface.
type p2pImpl struct {
	p2p     p2p.Client
	rqStore rqstore.Store
}

// NewP2PService returns a concrete implementation of P2PService.
func NewP2PService(client p2p.Client, store rqstore.Store) P2PService {
	return &p2pImpl{p2p: client, rqStore: store}
}

type StoreArtefactsRequest struct {
	TaskID     string
	ActionID   string
	IDFiles    [][]byte
	SymbolsDir string
}

// StoreArtefactsMetrics captures detailed outcomes of metadata and symbols storage.
type StoreArtefactsMetrics struct {
	// Metadata (ID files)
	MetaRate     float64 // percentage (0–100)
	MetaRequests int     // number of node RPCs attempted for metadata
	MetaCount    int     // number of metadata files attempted

	// Symbols
	SymRate     float64 // percentage (0–100) across all symbol batches (item-weighted)
	SymRequests int     // total node RPCs for symbol batches
	SymCount    int     // total symbols processed

	// Aggregated view
	AggregatedRate float64 // item-weighted across metadata and symbols
	TotalRequests  int     // MetaRequests + SymRequests

	// Durations (ms)
	MetaDurationMS int64
	SymDurationMS  int64

	// Per-RPC call details
	MetaCalls []StoreCallMetric
	SymCalls  []StoreCallMetric
}

// StoreCallMetric captures one per-node RPC attempt
type StoreCallMetric struct {
	IP         string
	ID         string
	Address    string
	Keys       int
	Success    bool
	Error      string
	DurationMS int64
}

func (p *p2pImpl) StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) (StoreArtefactsMetrics, error) {
	logtrace.Info(ctx, "About to store ID files", logtrace.Fields{"taskID": req.TaskID, "fileCount": len(req.IDFiles)})

	metaStart := time.Now()
	metaRate, metaReqs, metaCalls, err := p.storeCascadeMetadata(ctx, req.IDFiles, req.TaskID)
	if err != nil {
		return StoreArtefactsMetrics{}, errors.Wrap(err, "failed to store ID files")
	}
	logtrace.Info(ctx, "id files have been stored", f)
	metaDuration := time.Since(metaStart).Milliseconds()

	// NOTE: For now we aggregate by item count (ID files + symbol count).
	// TODO(move-to-request-weighted): Switch aggregation to request-weighted once
	// external consumers and metrics expectations are updated. We already return
	// totalRequests so the event/logs can include accurate request counts.
	symStart := time.Now()
	symRate, symCount, symReqs, symCalls, err := p.storeCascadeSymbols(ctx, req.TaskID, req.ActionID, req.SymbolsDir)
	if err != nil {
		return StoreArtefactsMetrics{}, errors.Wrap(err, "error storing raptor-q symbols")
	}
	logtrace.Info(ctx, "raptor-q symbols have been stored", f)
	symDuration := time.Since(symStart).Milliseconds()

	// Aggregate: weight by item counts (ID files + symbols) for now.
	metaCount := len(req.IDFiles)
	totalItems := metaCount + symCount
	aggRate := 0.0
	if totalItems > 0 {
		aggRate = ((metaRate * float64(metaCount)) + (symRate * float64(symCount))) / float64(totalItems)
	}
	totalRequests := metaReqs + symReqs
	return StoreArtefactsMetrics{
		MetaRate:       metaRate,
		MetaRequests:   metaReqs,
		MetaCount:      metaCount,
		SymRate:        symRate,
		SymRequests:    symReqs,
		SymCount:       symCount,
		AggregatedRate: aggRate,
		TotalRequests:  totalRequests,
		MetaDurationMS: metaDuration,
		SymDurationMS:  symDuration,
		MetaCalls:      metaCalls,
		SymCalls:       symCalls,
	}, nil
}

// storeCascadeMetadata stores cascade metadata (ID files) via P2P.
// Returns (ratePct, requests, error) as reported by the P2P client.
func (p *p2pImpl) storeCascadeMetadata(ctx context.Context, metadataFiles [][]byte, taskID string) (float64, int, []StoreCallMetric, error) {
	logtrace.Info(ctx, "Storing cascade metadata", logtrace.Fields{
		"taskID":    taskID,
		"fileCount": len(metadataFiles),
	})

	rate, reqs, calls, err := p.p2p.StoreBatch(ctx, metadataFiles, storage.P2PDataCascadeMetadata, taskID)
	if err != nil {
		return rate, reqs, nil, err
	}
	return rate, reqs, toAdaptorCalls(calls), nil
}

// storeCascadeSymbols loads symbols from `symbolsDir`, optionally downsamples,
// streams them in fixed-size batches to the P2P layer, and tracks:
// - an item-weighted aggregate success rate across all batches
// - the total number of symbols processed (item count)
// - the total number of node requests attempted across batches
//
// Returns (aggRate, totalSymbols, totalRequests, err).
func (p *p2pImpl) storeCascadeSymbols(ctx context.Context, taskID, actionID string, symbolsDir string) (float64, int, int, []StoreCallMetric, error) {
	/* record directory in DB */
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return 0, 0, 0, nil, fmt.Errorf("store symbol dir: %w", err)
	}

	/* gather every symbol path under symbolsDir ------------------------- */
	keys, err := walkSymbolTree(symbolsDir)
	if err != nil {
		return 0, 0, 0, nil, err
	}

	totalAvailable := len(keys)
	targetCount := int(math.Ceil(float64(totalAvailable) * storeSymbolsPercent / 100.0))
	if targetCount < 1 && totalAvailable > 0 {
		targetCount = 1
	}
	logtrace.Info(ctx, "first-pass target coverage (symbols)", logtrace.Fields{
		"total_symbols":  totalAvailable,
		"target_percent": storeSymbolsPercent,
		"target_count":   targetCount,
	})

	/* down-sample if we exceed the “big directory” threshold ------------- */
	if len(keys) > loadSymbolsBatchSize {
		want := targetCount
		if want < len(keys) {
			rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			keys = keys[:want]
		}
		sort.Strings(keys) // deterministic order inside the sample
	}

	logtrace.Info(ctx, "storing RaptorQ symbols", logtrace.Fields{"count": len(keys)})

	/* stream in fixed-size batches -------------------------------------- */
	sumWeightedRates := 0.0
	totalSymbols := 0
	totalRequests := 0
	var allCalls []StoreCallMetric
	for start := 0; start < len(keys); {
		end := start + loadSymbolsBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		rate, requests, count, calls, err := p.storeSymbolsInP2P(ctx, taskID, symbolsDir, batch)
		if err != nil {
			return rate, totalSymbols, totalRequests, allCalls, err
		}
		sumWeightedRates += rate * float64(count)
		totalSymbols += count
		totalRequests += requests
		allCalls = append(allCalls, calls...)
		start = end
	}

	achievedPct := 0.0
	if totalAvailable > 0 {
		achievedPct = (float64(totalSymbols) / float64(totalAvailable)) * 100.0
	}
	logtrace.Info(ctx, "first-pass achieved coverage (symbols)", logtrace.Fields{
		"achieved_symbols": totalSymbols,
		"achieved_percent": achievedPct,
		"total_requests":   totalRequests,
	})

	if err := p.rqStore.UpdateIsFirstBatchStored(actionID); err != nil {
		return 0, totalSymbols, totalRequests, allCalls, fmt.Errorf("update first-batch flag: %w", err)
	}
	logtrace.Info(ctx, "finished storing RaptorQ symbols", logtrace.Fields{
		"curr-time": time.Now().UTC(),
		"count":     len(keys),
	})

	aggRate := 0.0
	if totalSymbols > 0 {
		aggRate = sumWeightedRates / float64(totalSymbols)
	}
	return aggRate, totalSymbols, totalRequests, allCalls, nil
}

func walkSymbolTree(root string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // propagate I/O errors
		}
		if d.IsDir() {
			return nil // skip directory nodes
		}
		// ignore layout json if present
		if strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		keys = append(keys, rel) // store as "block_0/filename"
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk symbol tree: %w", err)
	}
	return keys, nil
}

// storeSymbolsInP2P loads a batch of symbols and stores them via P2P.
// Returns (ratePct, requests, count, error) where `count` is the number of symbols in this batch.
func (c *p2pImpl) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) (float64, int, int, []StoreCallMetric, error) {
	logtrace.Info(ctx, "loading batch symbols", logtrace.Fields{"count": len(fileKeys)})

	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("load symbols: %w", err)
	}

	symCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	rate, requests, calls, err := c.p2p.StoreBatch(symCtx, symbols, storage.P2PDataRaptorQSymbol, taskID)
	if err != nil {
		return rate, requests, len(symbols), nil, fmt.Errorf("p2p store batch: %w", err)
	}
	logtrace.Info(ctx, "stored batch symbols", logtrace.Fields{"count": len(symbols)})

	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return rate, requests, len(symbols), nil, fmt.Errorf("delete symbols: %w", err)
	}
	logtrace.Info(ctx, "deleted batch symbols", logtrace.Fields{"count": len(symbols)})

	return rate, requests, len(symbols), toAdaptorCalls(calls), nil
}

// toAdaptorCalls converts DHT call metrics to adaptor-level metrics
func toAdaptorCalls(in []kademlia.StoreCallMetric) []StoreCallMetric {
	out := make([]StoreCallMetric, 0, len(in))
	for _, c := range in {
		out = append(out, StoreCallMetric{
			IP:         c.IP,
			ID:         c.ID,
			Address:    c.Address,
			Keys:       c.Keys,
			Success:    c.Success,
			Error:      c.Error,
			DurationMS: c.DurationMS,
		})
	}
	return out
}
