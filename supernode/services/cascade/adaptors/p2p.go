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
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/storage"
	"github.com/pkg/errors"
)

const (
	loadSymbolsBatchSize = 5000
	// Minimum first-pass coverage to store before returning from Register (percent)
	storeSymbolsPercent = 18

	storeBatchContextTimeout = 3 * time.Minute
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
}

func (p *p2pImpl) StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) (StoreArtefactsMetrics, error) {
	logtrace.Info(ctx, "About to store ID files", logtrace.Fields{"taskID": req.TaskID, "fileCount": len(req.IDFiles)})
	// NOTE: For now we aggregate by item count (ID files + symbol count).
	// TODO(move-to-request-weighted): Switch aggregation to request-weighted once
	// external consumers and metrics expectations are updated. We already return
	// totalRequests so the event/logs can include accurate request counts.
	symRate, symCount, symReqs, err := p.storeCascadeSymbolsAndData(ctx, req.TaskID, req.ActionID, req.SymbolsDir, req.IDFiles)
	if err != nil {
		return StoreArtefactsMetrics{}, errors.Wrap(err, "error storing raptor-q symbols")
	}
	logtrace.Info(ctx, "raptor-q symbols have been stored", f)

	return StoreArtefactsMetrics{
		SymRate:     symRate,
		SymRequests: symReqs,
		SymCount:    symCount,
	}, nil
}

// storeCascadeSymbols loads symbols from `symbolsDir`, optionally downsamples,
// streams them in fixed-size batches to the P2P layer, and tracks:
// - an item-weighted aggregate success rate across all batches
// - the total number of symbols processed (item count)
// - the total number of node requests attempted across batches
//
// Returns (aggRate, totalSymbols, totalRequests, err).
func (p *p2pImpl) storeCascadeSymbolsAndData(ctx context.Context, taskID, actionID string, symbolsDir string, metadataFiles [][]byte) (float64, int, int, error) {
	/* record directory in DB */
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return 0, 0, 0, fmt.Errorf("store symbol dir: %w", err)
	}

	/* gather every symbol path under symbolsDir ------------------------- */
	keys, err := walkSymbolTree(symbolsDir)
	if err != nil {
		return 0, 0, 0, err
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
	totalSymbols := 0 // symbols only
	totalItems := 0   // symbols + metadata (for rate weighting)
	totalRequests := 0
	firstBatchProcessed := false

	for start := 0; start < len(keys); {
		end := min(start+loadSymbolsBatchSize, len(keys))
		batch := keys[start:end]

		if !firstBatchProcessed && len(metadataFiles) > 0 {
			// First "batch" has to include metadata + as many symbols as fit under batch size.
			// If metadataFiles >= batch size, we send metadata in this batch and symbols start next batch.
			roomForSymbols := loadSymbolsBatchSize - len(metadataFiles)
			if roomForSymbols < 0 {
				roomForSymbols = 0
			}
			if roomForSymbols < len(batch) {
				// trim the first symbol chunk to leave space for metadata
				batch = batch[:roomForSymbols]
				end = start + roomForSymbols
			}

			// Load just this symbol chunk
			symBytes, err := utils.LoadSymbols(symbolsDir, batch)
			if err != nil {
				return 0, totalSymbols, totalRequests, fmt.Errorf("load symbols: %w", err)
			}

			// Build combined payload: metadata first, then symbols
			payload := make([][]byte, 0, len(metadataFiles)+len(symBytes))
			payload = append(payload, metadataFiles...)
			payload = append(payload, symBytes...)

			// Send as the same data type you use for symbols
			bctx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
			rate, reqs, err := p.p2p.StoreBatch(bctx, payload, storage.P2PDataRaptorQSymbol, taskID)
			cancel()
			if err != nil {
				agg := 0.0
				if totalItems > 0 {
					agg = sumWeightedRates / float64(totalItems)
				}
				return agg, totalSymbols, totalRequests + reqs, fmt.Errorf("p2p store batch (first): %w", err)
			}

			// Metrics
			items := len(payload) // meta + symbols
			sumWeightedRates += rate * float64(items)
			totalItems += items
			totalSymbols += len(symBytes)
			totalRequests += reqs

			// Delete only the symbols we uploaded
			if len(batch) > 0 {
				if err := utils.DeleteSymbols(ctx, symbolsDir, batch); err != nil {
					return rate, totalSymbols, totalRequests, fmt.Errorf("delete symbols: %w", err)
				}
			}

			firstBatchProcessed = true
		} else {
			rate, requests, count, err := p.storeSymbolsInP2P(ctx, taskID, symbolsDir, batch)
			if err != nil {
				agg := 0.0
				if totalItems > 0 {
					agg = sumWeightedRates / float64(totalItems)
				}
				return agg, totalSymbols, totalRequests, err
			}
			sumWeightedRates += rate * float64(count)
			totalItems += count
			totalSymbols += count
			totalRequests += requests
		}

		start = end
	}

	// Coverage uses symbols only
	achievedPct := 0.0
	if totalAvailable > 0 {
		achievedPct = (float64(totalSymbols) / float64(totalAvailable)) * 100.0
	}
	logtrace.Info(ctx, "first-pass achieved coverage (symbols)",
		logtrace.Fields{"achieved_symbols": totalSymbols, "achieved_percent": achievedPct, "total_requests": totalRequests})

	if err := p.rqStore.UpdateIsFirstBatchStored(actionID); err != nil {
		return 0, totalSymbols, totalRequests, fmt.Errorf("update first-batch flag: %w", err)
	}

	aggRate := 0.0
	if totalItems > 0 {
		aggRate = sumWeightedRates / float64(totalItems)
	}
	return aggRate, totalSymbols, totalRequests, nil

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
func (c *p2pImpl) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) (float64, int, int, error) {
	logtrace.Info(ctx, "loading batch symbols", logtrace.Fields{"count": len(fileKeys)})

	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load symbols: %w", err)
	}

	symCtx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
	defer cancel()

	rate, requests, err := c.p2p.StoreBatch(symCtx, symbols, storage.P2PDataRaptorQSymbol, taskID)
	if err != nil {
		return rate, requests, len(symbols), fmt.Errorf("p2p store batch: %w", err)
	}
	logtrace.Info(ctx, "stored batch symbols", logtrace.Fields{"count": len(symbols)})

	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return rate, requests, len(symbols), fmt.Errorf("delete symbols: %w", err)
	}
	logtrace.Info(ctx, "deleted batch symbols", logtrace.Fields{"count": len(symbols)})

	return rate, requests, len(symbols), nil
}
