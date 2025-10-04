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
)

const (
	loadSymbolsBatchSize     = 1000
	storeSymbolsPercent      = 18
	storeBatchContextTimeout = 3 * time.Minute
	P2PDataRaptorQSymbol     = 1
)

type P2PService interface {
	StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) error
}

type p2pImpl struct {
	p2p     p2p.Client
	rqStore rqstore.Store
}

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
		return fmt.Errorf("error storing artefacts: %w", err)
	}
	_ = firstPassSymbols
	_ = totalSymbols
	_ = start
	remaining := 0
	if req.SymbolsDir != "" {
		if keys, werr := walkSymbolTree(req.SymbolsDir); werr == nil {
			remaining = len(keys)
		}
	}
	logtrace.Info(ctx, "store: first-pass complete", logtrace.Fields{"taskID": req.TaskID, "symbols_first_pass": firstPassSymbols, "symbols_total_available": totalSymbols, "id_files_count": len(req.IDFiles), "symbols_left_on_disk": remaining, "ms": time.Since(start).Milliseconds()})
	if remaining == 0 {
		logtrace.Info(ctx, "store: dir empty after first-pass", logtrace.Fields{"taskID": req.TaskID, "dir": req.SymbolsDir})
	}
	return nil
}

func (p *p2pImpl) storeCascadeSymbolsAndData(ctx context.Context, taskID, actionID string, symbolsDir string, metadataFiles [][]byte) (int, int, error) {
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return 0, 0, fmt.Errorf("store symbol dir: %w", err)
	}
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
	logtrace.Info(ctx, "store: target coverage", logtrace.Fields{"total_symbols": totalAvailable, "target_percent": storeSymbolsPercent, "target_count": targetCount})
	if len(keys) > loadSymbolsBatchSize {
		want := targetCount
		if want < len(keys) {
			rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			keys = keys[:want]
		}
		sort.Strings(keys)
	}
	logtrace.Info(ctx, "store: selected symbols", logtrace.Fields{"selected": len(keys), "of_total": totalAvailable, "dir": symbolsDir})
	logtrace.Info(ctx, "store: sending symbols", logtrace.Fields{"count": len(keys)})
	totalSymbols := 0
	firstBatchProcessed := false
	for start := 0; start < len(keys); {
		end := min(start+loadSymbolsBatchSize, len(keys))
		batch := keys[start:end]
		if !firstBatchProcessed && len(metadataFiles) > 0 {
			roomForSymbols := loadSymbolsBatchSize - len(metadataFiles)
			if roomForSymbols < 0 {
				roomForSymbols = 0
			}
			if roomForSymbols < len(batch) {
				batch = batch[:roomForSymbols]
				end = start + roomForSymbols
			}
			symBytes, err := utils.LoadSymbols(symbolsDir, batch)
			if err != nil {
				return 0, 0, fmt.Errorf("load symbols: %w", err)
			}
			payload := make([][]byte, 0, len(metadataFiles)+len(symBytes))
			payload = append(payload, metadataFiles...)
			payload = append(payload, symBytes...)
			logtrace.Info(ctx, "store: batch send (first)", logtrace.Fields{"taskID": taskID, "metadata_count": len(metadataFiles), "symbols_in_batch": len(symBytes), "payload_total": len(payload)})
			bctx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
			err = p.p2p.StoreBatch(bctx, payload, P2PDataRaptorQSymbol, taskID)
			cancel()
			if err != nil {
				return totalSymbols, totalAvailable, fmt.Errorf("p2p store batch (first): %w", err)
			}
			logtrace.Info(ctx, "store: batch ok (first)", logtrace.Fields{"taskID": taskID, "symbols_stored": len(symBytes)})
			totalSymbols += len(symBytes)
			if len(batch) > 0 {
				if err := utils.DeleteSymbols(ctx, symbolsDir, batch); err != nil {
					return totalSymbols, totalAvailable, fmt.Errorf("delete symbols: %w", err)
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
	if err := p.rqStore.UpdateIsFirstBatchStored(actionID); err != nil {
		return totalSymbols, totalAvailable, fmt.Errorf("update first-batch flag: %w", err)
	}
	return totalSymbols, totalAvailable, nil
}

func walkSymbolTree(root string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".json") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		keys = append(keys, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk symbol tree: %w", err)
	}
	return keys, nil
}

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
	return len(symbols), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
