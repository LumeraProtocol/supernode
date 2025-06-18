package kademlia

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

const (
	defaultSoreSymbolsInterval = 30 * time.Second
	loadSymbolsBatchSize       = 1000
)

func (s *DHT) startStoreSymbolsWorker(ctx context.Context) {
	logtrace.Info(ctx, "start delete data worker", logtrace.Fields{logtrace.FieldModule: "p2p"})

	for {
		select {
		case <-time.After(defaultSoreSymbolsInterval):
			if err := s.storeSymbols(ctx); err != nil {
				logtrace.Error(ctx, "store symbols", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
			}
		case <-ctx.Done():
			logtrace.Error(ctx, "closing store symbols worker", logtrace.Fields{logtrace.FieldModule: "p2p"})
			return
		}
	}
}

func (s *DHT) storeSymbols(ctx context.Context) error {
	dirs, err := s.rqstore.GetToDoStoreSymbolDirs()
	if err != nil {
		return fmt.Errorf("get to do store symbol dirs: %w", err)
	}

	for _, dir := range dirs {
		logtrace.Info(ctx, "rq_symbols worker: start scanning dir & storing raptorQ symbols", logtrace.Fields{"dir": dir, "txid": dir.TXID})
		if err := s.scanDirAndStoreSymbols(ctx, dir.Dir, dir.TXID); err != nil {
			logtrace.Error(ctx, "scan and store symbols", logtrace.Fields{logtrace.FieldModule: "p2p", logtrace.FieldError: err})
		}

		logtrace.Info(ctx, "rq_symbols worker: scanned dir & stored raptorQ symbols", logtrace.Fields{"dir": dir, "txid": dir.TXID})
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
		if err := s.storeSymbolsInP2P(ctx, dir, keys[start:end]); err != nil {
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
func (s *DHT) storeSymbolsInP2P(ctx context.Context, dir string, keys []string) error {
	loaded, err := utils.LoadSymbols(dir, keys)
	if err != nil {
		return fmt.Errorf("load symbols: %w", err)
	}

	if err := s.StoreBatch(ctx, loaded, 1, dir); err != nil {
		return fmt.Errorf("p2p store batch: %w", err)
	}

	if err := utils.DeleteSymbols(ctx, dir, keys); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	return nil
}
