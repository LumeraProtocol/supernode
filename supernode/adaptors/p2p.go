package adaptors

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	loadSymbolsBatchSize     = 100
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
	Layout     codec.Layout
}

func (p *p2pImpl) StoreArtefacts(ctx context.Context, req StoreArtefactsRequest, f logtrace.Fields) error {
	idFilesBytes := totalBytes(req.IDFiles)
	logtrace.Info(ctx, "store: p2p start", logtrace.Fields{
		"taskID":          req.TaskID,
		"actionID":        req.ActionID,
		"id_files":        len(req.IDFiles),
		"id_files_bytes":  idFilesBytes,
		"id_files_mb_est": utils.BytesIntToMB(idFilesBytes),
		"symbols_dir":     req.SymbolsDir,
	})
	start := time.Now()
	firstPassSymbols, totalSymbols, err := p.storeCascadeSymbolsAndData(ctx, req.TaskID, req.ActionID, req.SymbolsDir, req.IDFiles, req.Layout)
	if err != nil {
		return fmt.Errorf("error storing artefacts: %w", err)
	}
	remainingEst := totalSymbols - firstPassSymbols
	if remainingEst < 0 {
		remainingEst = 0
	}
	logtrace.Info(ctx, "store: first-pass complete", logtrace.Fields{
		"taskID":                   req.TaskID,
		"symbols_first_pass":       firstPassSymbols,
		"symbols_total_available":  totalSymbols,
		"id_files_count":           len(req.IDFiles),
		"symbols_left_on_disk_est": remainingEst,
		"ms":                       time.Since(start).Milliseconds(),
	})
	if remainingEst == 0 {
		logtrace.Info(ctx, "store: dir empty after first-pass", logtrace.Fields{"taskID": req.TaskID, "dir": req.SymbolsDir})
	}
	return nil
}

func (p *p2pImpl) storeCascadeSymbolsAndData(ctx context.Context, taskID, actionID string, symbolsDir string, metadataFiles [][]byte, layout codec.Layout) (int, int, error) {
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return 0, 0, fmt.Errorf("store symbol dir: %w", err)
	}
	metadataBytes := totalBytes(metadataFiles)
	keys, err := symbolKeysFromLayout(layout)
	if err != nil {
		logtrace.Warn(ctx, "store: layout keys unavailable; falling back to disk scan", logtrace.Fields{
			"taskID": taskID,
			"dir":    symbolsDir,
			"err":    err.Error(),
		})
		if symbolsDir == "" {
			return 0, 0, err
		}
		keySet, derr := utils.ReadDirFilenames(symbolsDir)
		if derr != nil {
			return 0, 0, fmt.Errorf("symbol keys from layout: %w; dir scan: %v", err, derr)
		}
		keys = make([]string, 0, len(keySet))
		for k := range keySet {
			keys = append(keys, k)
		}
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
	totalBytesStored := 0
	metadataBytesStored := 0
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
			symBytesLen := totalBytes(symBytes)
			payload := make([][]byte, 0, len(metadataFiles)+len(symBytes))
			payload = append(payload, metadataFiles...)
			payload = append(payload, symBytes...)
			logtrace.Info(ctx, "store: batch send (first)", logtrace.Fields{
				"taskID":           taskID,
				"metadata_count":   len(metadataFiles),
				"metadata_bytes":   metadataBytes,
				"metadata_mb_est":  utils.BytesIntToMB(metadataBytes),
				"symbols_in_batch": len(symBytes),
				"symbols_bytes":    symBytesLen,
				"symbols_mb_est":   utils.BytesIntToMB(symBytesLen),
				"payload_total":    len(payload),
				"payload_bytes":    metadataBytes + symBytesLen,
				"payload_mb_est":   utils.BytesIntToMB(metadataBytes + symBytesLen),
			})
			bctx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
			err = p.p2p.StoreBatch(bctx, payload, P2PDataRaptorQSymbol, taskID)
			cancel()
			if err != nil {
				return totalSymbols, totalAvailable, fmt.Errorf("p2p store batch (first): %w", err)
			}
			logtrace.Info(ctx, "store: batch ok (first)", logtrace.Fields{
				"taskID":         taskID,
				"symbols_stored": len(symBytes),
				"symbols_bytes":  symBytesLen,
				"payload_bytes":  metadataBytes + symBytesLen,
			})
			totalSymbols += len(symBytes)
			totalBytesStored += symBytesLen + metadataBytes
			metadataBytesStored += metadataBytes
			if len(batch) > 0 {
				if err := utils.DeleteSymbols(ctx, symbolsDir, batch); err != nil {
					return totalSymbols, totalAvailable, fmt.Errorf("delete symbols: %w", err)
				}
			}
			firstBatchProcessed = true
		} else {
			count, bytes, err := p.storeSymbolsInP2P(ctx, taskID, symbolsDir, batch)
			if err != nil {
				return totalSymbols, totalAvailable, err
			}
			totalSymbols += count
			totalBytesStored += bytes
		}
		start = end
	}
	if err := utils.PruneEmptyBlockDirs(symbolsDir); err != nil {
		logtrace.Warn(ctx, "store: prune block dirs failed", logtrace.Fields{"taskID": taskID, "dir": symbolsDir, logtrace.FieldError: err.Error()})
	}
	if err := p.rqStore.UpdateIsFirstBatchStored(taskID); err != nil {
		return totalSymbols, totalAvailable, fmt.Errorf("update first-batch flag: %w", err)
	}
	logtrace.Info(ctx, "store: first-pass bytes summary", logtrace.Fields{
		"taskID":               taskID,
		"symbols_stored":       totalSymbols,
		"symbols_total":        totalAvailable,
		"symbols_bytes_stored": totalBytesStored - metadataBytesStored,
		"metadata_bytes":       metadataBytes,
		"payload_bytes_stored": totalBytesStored,
		"payload_mb_est":       utils.BytesIntToMB(totalBytesStored),
	})
	return totalSymbols, totalAvailable, nil
}

func symbolKeysFromLayout(layout codec.Layout) ([]string, error) {
	if len(layout.Blocks) == 0 {
		return nil, fmt.Errorf("empty layout: no blocks")
	}
	keys := make([]string, 0, 1024)
	seen := make(map[string]struct{}, 1024)
	for _, block := range layout.Blocks {
		blockDir := fmt.Sprintf("block_%d", block.BlockID)
		for _, symbolID := range block.Symbols {
			if symbolID == "" {
				continue
			}
			k := filepath.Join(blockDir, symbolID)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (c *p2pImpl) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) (count int, bytes int, err error) {
	logtrace.Debug(ctx, "loading batch symbols", logtrace.Fields{"taskID": taskID, "count": len(fileKeys)})
	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return 0, 0, fmt.Errorf("load symbols: %w", err)
	}
	symbolsBytes := totalBytes(symbols)
	symCtx, cancel := context.WithTimeout(ctx, storeBatchContextTimeout)
	defer cancel()
	logtrace.Info(ctx, "store: batch send (symbols)", logtrace.Fields{
		"taskID":           taskID,
		"symbols_in_batch": len(symbols),
		"symbols_bytes":    symbolsBytes,
		"symbols_mb_est":   utils.BytesIntToMB(symbolsBytes),
	})
	if err := c.p2p.StoreBatch(symCtx, symbols, P2PDataRaptorQSymbol, taskID); err != nil {
		return len(symbols), symbolsBytes, fmt.Errorf("p2p store batch: %w", err)
	}
	logtrace.Info(ctx, "store: batch ok (symbols)", logtrace.Fields{
		"taskID":         taskID,
		"symbols_stored": len(symbols),
		"symbols_bytes":  symbolsBytes,
	})
	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return len(symbols), symbolsBytes, fmt.Errorf("delete symbols: %w", err)
	}
	return len(symbols), symbolsBytes, nil
}

func totalBytes(chunks [][]byte) int {
	// totalBytes sums the byte lengths of each blob in a slice.
	// It is used for store telemetry and batch sizing diagnostics.
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	return total
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
