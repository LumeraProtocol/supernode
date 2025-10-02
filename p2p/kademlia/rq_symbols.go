package kademlia

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

const (
	defaultSoreSymbolsInterval = 30 * time.Second
	loadSymbolsBatchSize       = 1000
)

func (s *DHT) startStoreSymbolsWorker(ctx context.Context) {
	// Minimal visibility for lifecycle + each tick
	logtrace.Debug(ctx, "rq_symbols worker started", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultSoreSymbolsInterval):
			if err := s.storeSymbols(ctx); err != nil {
				logtrace.Error(ctx, "store symbols", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
			}
		case <-ctx.Done():
			logtrace.Debug(ctx, "rq_symbols worker stopping", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return
		}
	}
}

func (s *DHT) storeSymbols(ctx context.Context) error {
	dirs, err := s.rqstore.GetToDoStoreSymbolDirs()
	if err != nil {
		return fmt.Errorf("get to do store symbol dirs: %w", err)
	}

	// Minimal visibility: how many dirs to process this tick
	if len(dirs) > 0 {
		logtrace.Info(ctx, "worker: symbols todo", logtrace.Fields{"count": len(dirs)})
	}

	for _, dir := range dirs {
		// Use txid as correlation id so worker logs join with register flow
		wctx := logtrace.CtxWithCorrelationID(ctx, dir.TXID)
		// Pre-count symbols in this directory
		preCount := -1
		if set, rerr := utils.ReadDirFilenames(dir.Dir); rerr == nil {
			preCount = len(set)
		}
		start := time.Now()
		logtrace.Info(wctx, "worker: dir start", logtrace.Fields{"dir": dir.Dir, "txid": dir.TXID, "symbols": preCount})
        if err := s.scanDirAndStoreSymbols(wctx, dir.Dir, dir.TXID); err != nil {
            logtrace.Error(wctx, "scan and store symbols", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
        }
		// Post-count remaining symbols
		remCount := -1
		if set, rerr := utils.ReadDirFilenames(dir.Dir); rerr == nil {
			remCount = len(set)
		}
		logtrace.Info(wctx, "worker: dir done", logtrace.Fields{"dir": dir.Dir, "txid": dir.TXID, "remaining": remCount, "ms": time.Since(start).Milliseconds()})
	}

	return nil
}

// ---------------------------------------------------------------------
// 1. Scan dir → send ALL symbols (no sampling)
// ---------------------------------------------------------------------
func (s *DHT) scanDirAndStoreSymbols(ctx context.Context, dir, txid string) error {
	// Collect relative file paths like "block_0/foo.sym"
	keySet, err := utils.ReadDirFilenames(dir)
	if err != nil {
		return fmt.Errorf("read dir filenames: %w", err)
	}

	// Turn the set into a sorted slice for deterministic batching
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	logtrace.Info(ctx, "p2p-worker: storing ALL RaptorQ symbols", logtrace.Fields{"txid": txid, "dir": dir, "total": len(keys)})

    // Batch-flush at loadSymbolsBatchSize
    for start := 0; start < len(keys); {
        end := start + loadSymbolsBatchSize
        if end > len(keys) {
            end = len(keys)
        }
        if err := s.storeSymbolsInP2P(ctx, txid, dir, keys[start:end]); err != nil {
            return err
        }
        start = end
    }

	// Mark this directory as completed in rqstore
	if err := s.rqstore.SetIsCompleted(txid); err != nil {
		return fmt.Errorf("set is-completed: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------
// 2. Load → StoreBatch → Delete for a slice of keys
// ---------------------------------------------------------------------
func (s *DHT) storeSymbolsInP2P(ctx context.Context, txid, dir string, keys []string) error {
	// Per-batch visibility for background worker
    logtrace.Info(ctx, "worker: batch send", logtrace.Fields{"dir": dir, "keys": len(keys), logtrace.FieldTaskID: txid})

	start := time.Now()
	loaded, err := utils.LoadSymbols(dir, keys)
	if err != nil {
		return fmt.Errorf("load symbols: %w", err)
	}

    if err := s.StoreBatch(ctx, loaded, 1, txid); err != nil {
        return fmt.Errorf("p2p store batch: %w", err)
    }

    logtrace.Info(ctx, "worker: batch ok", logtrace.Fields{"dir": dir, "keys": len(loaded), "ms": time.Since(start).Milliseconds(), logtrace.FieldTaskID: txid})

	if err := utils.DeleteSymbols(ctx, dir, keys); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	return nil
}
