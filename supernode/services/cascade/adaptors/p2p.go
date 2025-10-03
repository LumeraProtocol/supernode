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
	"github.com/pkg/errors"
)

const (
	loadSymbolsBatchSize = 3000
	// Minimum first-pass coverage to store before returning from Register (percent)
	storeSymbolsPercent = 18

	storeBatchContextTimeout = 3 * time.Minute
)

// Local P2P data type identifier used when storing via P2P.
// Value must remain stable to preserve DB semantics.
const P2PDataRaptorQSymbol = 1

// P2PService defines the interface for storing data in the P2P layer.
//
//go:generate mockgen -destination=mocks/p2p_mock.go -package=cascadeadaptormocks -source=p2p.go
type P2PService interface {
	// StoreArtefacts stores ID files and RaptorQ symbols.
	// Metrics are recorded via internal metrics helpers; no metrics are returned.
	StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) error
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

func (p *p2pImpl) StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) error {
	logtrace.Info(ctx, "store: p2p start", logtrace.Fields{"taskID": req.TaskID, "actionID": req.ActionID, "id_files": len(req.IDFiles), "symbols_dir": req.SymbolsDir})

	start := time.Now()
	firstPassSymbols, totalSymbols, err := p.storeCascadeSymbolsAndData(ctx, req.TaskID, req.ActionID, req.SymbolsDir, req.IDFiles)
	if err != nil {
		return errors.Wrap(err, "error storing artefacts")
	}
	dur := time.Since(start).Milliseconds()
	// After first-pass, log how many symbols remain on disk
	remaining := 0
	if req.SymbolsDir != "" {
		if keys, werr := walkSymbolTree(req.SymbolsDir); werr == nil {
			remaining = len(keys)
		}
	}
	logtrace.Info(ctx, "store: first-pass complete", logtrace.Fields{"taskID": req.TaskID, "symbols_first_pass": firstPassSymbols, "symbols_total_available": totalSymbols, "id_files_count": len(req.IDFiles), "symbols_left_on_disk": remaining, "ms": dur})
	if remaining == 0 {
		logtrace.Info(ctx, "store: dir empty after first-pass", logtrace.Fields{"taskID": req.TaskID, "dir": req.SymbolsDir})
	}
	// Metrics collection removed; logs retained
	return nil
}

// storeCascadeSymbols loads symbols from `symbolsDir`, optionally downsamples,
// streams them in fixed-size batches to the P2P layer, and tracks:
// - an item-weighted aggregate success rate across all batches
// - the total number of symbols processed (item count)
// - the total number of node requests attempted across batches
//
// Returns (aggRate, totalSymbols, totalRequests, err).
func (p *p2pImpl) storeCascadeSymbolsAndData(ctx context.Context, taskID, actionID string, symbolsDir string, metadataFiles [][]byte) (int, int, error) {
	/* record directory in DB */
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return 0, 0, fmt.Errorf("store symbol dir: %w", err)
	}

	/* gather every symbol path under symbolsDir ------------------------- */
	keys, err := walkSymbolTree(symbolsDir)
	if err != nil {
		return 0, 0, err
	}

	totalAvailable := len(keys)
	targetCount := int(math.Ceil(float64(totalAvailable) * storeSymbolsPercent / 100.0))
	if targetCount < 1 && totalAvailable > 0 {
		targetCount = 1
	}
	logtrace.Info(ctx, "store: symbols discovered", logtrace.Fields{"total_symbols": totalAvailable, "dir": symbolsDir})
	logtrace.Info(ctx, "store: target coverage", logtrace.Fields{
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
	logtrace.Info(ctx, "store: selected symbols", logtrace.Fields{"selected": len(keys), "of_total": totalAvailable, "dir": symbolsDir})
	logtrace.Info(ctx, "store: sending symbols", logtrace.Fields{"count": len(keys)})

	/* stream in fixed-size batches -------------------------------------- */

	totalSymbols := 0 // symbols stored
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
				return 0, 0, fmt.Errorf("load symbols: %w", err)
			}

			// Build combined payload: metadata first, then symbols
			payload := make([][]byte, 0, len(metadataFiles)+len(symBytes))
			payload = append(payload, metadataFiles...)
			payload = append(payload, symBytes...)

			// Send as the same data type you use for symbols
			logtrace.Info(ctx, "store: batch send (first)", logtrace.Fields{"taskID": taskID, "metadata_count": len(metadataFiles), "symbols_in_batch": len(symBytes), "payload_total": len(payload)})
			bctx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
			err = p.p2p.StoreBatch(bctx, payload, P2PDataRaptorQSymbol, taskID)
			cancel()
			if err != nil {
				return totalSymbols, totalAvailable, fmt.Errorf("p2p store batch (first): %w", err)
			}
			logtrace.Info(ctx, "store: batch ok (first)", logtrace.Fields{"taskID": taskID, "symbols_stored": len(symBytes)})

			totalSymbols += len(symBytes)
			// No per-RPC metrics propagated from p2p

			// Delete only the symbols we uploaded
			if len(batch) > 0 {
				if err := utils.DeleteSymbols(ctx, symbolsDir, batch); err != nil {
					return totalSymbols, totalAvailable, fmt.Errorf("delete symbols: %w", err)
				}
			}
			// Log remaining symbols in directory after deletion
			if rem, werr := walkSymbolTree(symbolsDir); werr == nil {
				if left := len(rem); left > 0 {
					logtrace.Info(ctx, "store: remaining after first batch", logtrace.Fields{"taskID": taskID, "left": left})
				} else {
					logtrace.Info(ctx, "store: dir empty after first batch", logtrace.Fields{"taskID": taskID, "dir": symbolsDir})
				}
			}

			firstBatchProcessed = true
		} else {
			count, err := p.storeSymbolsInP2P(ctx, taskID, symbolsDir, batch)
			if err != nil {
				return totalSymbols, totalAvailable, err
			}
			totalSymbols += count
		}

		start = end
	}

	// Coverage uses symbols only
	achievedPct := 0.0
	if totalAvailable > 0 {
		achievedPct = (float64(totalSymbols) / float64(totalAvailable)) * 100.0
	}
	logtrace.Info(ctx, "store: coverage", logtrace.Fields{"achieved_symbols": totalSymbols, "achieved_percent": achievedPct})

	if err := p.rqStore.UpdateIsFirstBatchStored(actionID); err != nil {
		return totalSymbols, totalAvailable, fmt.Errorf("update first-batch flag: %w", err)
	}
	// Final remaining count after first pass flagged
	if rem, werr := walkSymbolTree(symbolsDir); werr == nil {
		if left := len(rem); left > 0 {
			logtrace.Info(ctx, "store: remaining after first-pass", logtrace.Fields{"taskID": taskID, "left": left, "dir": symbolsDir})
		} else {
			logtrace.Info(ctx, "store: directory empty after first-pass", logtrace.Fields{"taskID": taskID, "dir": symbolsDir})
		}
	}

	return totalSymbols, totalAvailable, nil

}

// walkSymbolTree returns relative file keys for symbols under `root`,
// skipping JSON layout files.
func walkSymbolTree(root string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // propagate I/O errors
		}
		if d.IsDir() {
			return nil // skip directories
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
func (c *p2pImpl) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) (int, error) {
	logtrace.Debug(ctx, "loading batch symbols", logtrace.Fields{"taskID": taskID, "count": len(fileKeys)})

	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return 0, fmt.Errorf("load symbols: %w", err)
	}

	symCtx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
	defer cancel()

	logtrace.Info(ctx, "store: batch send (symbols)", logtrace.Fields{"taskID": taskID, "symbols_in_batch": len(symbols)})
	if err := c.p2p.StoreBatch(symCtx, symbols, P2PDataRaptorQSymbol, taskID); err != nil {
		return len(symbols), fmt.Errorf("p2p store batch: %w", err)
	}
	logtrace.Info(ctx, "store: batch ok (symbols)", logtrace.Fields{"taskID": taskID, "symbols_stored": len(symbols)})

	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return len(symbols), fmt.Errorf("delete symbols: %w", err)
	}
	// After deletion, log remaining count in directory
	left := -1
	if rem, werr := walkSymbolTree(root); werr == nil {
		left = len(rem)
	}
	logtrace.Debug(ctx, "deleted batch symbols", logtrace.Fields{"taskID": taskID, "count": len(symbols), "symbols_left_on_disk": left})

	// No per-RPC metrics propagated from p2p
	return len(symbols), nil
}
