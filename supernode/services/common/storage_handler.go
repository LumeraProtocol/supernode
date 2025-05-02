package common

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

	"github.com/LumeraProtocol/supernode/p2p"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/storage/files"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

const (
	loadSymbolsBatchSize = 2500
	storeSymbolsPercent  = 10
	concurrency          = 1
)

// StorageHandler provides common logic for RQ and P2P operations
type StorageHandler struct {
	P2PClient p2p.Client
	rqDir     string

	TaskID string
	TxID   string

	store     rqstore.Store
	semaphore chan struct{}
}

// NewStorageHandler creates instance of StorageHandler
func NewStorageHandler(p2p p2p.Client, rqDir string, store rqstore.Store) *StorageHandler {
	return &StorageHandler{
		P2PClient: p2p,
		rqDir:     rqDir,
		store:     store,
		semaphore: make(chan struct{}, concurrency),
	}
}

// StoreFileIntoP2P stores file into P2P
func (h *StorageHandler) StoreFileIntoP2P(ctx context.Context, file *files.File, typ int) (string, error) {
	data, err := file.Bytes()
	if err != nil {
		return "", errors.Errorf("store file %s into p2p", file.Name())
	}
	return h.StoreBytesIntoP2P(ctx, data, typ)
}

// StoreBytesIntoP2P into P2P actual data
func (h *StorageHandler) StoreBytesIntoP2P(ctx context.Context, data []byte, typ int) (string, error) {
	return h.P2PClient.Store(ctx, data, typ)
}

// StoreBatch stores into P2P array of bytes arrays
func (h *StorageHandler) StoreBatch(ctx context.Context, list [][]byte, typ int) error {
	val := ctx.Value(log.TaskIDKey)
	taskID := ""
	if val != nil {
		taskID = fmt.Sprintf("%v", val)
	}
	log.WithContext(ctx).WithField("task_id", taskID).Info("task_id in storeList")

	return h.P2PClient.StoreBatch(ctx, list, typ, taskID)
}

// StoreRaptorQSymbolsIntoP2P stores RaptorQ symbols into P2P
// It first records the directory in the database, then gathers all symbol paths
// under the specified directory. If the number of keys exceeds a certain threshold,
// it randomly samples a percentage of them. Finally, it streams the symbols in
// fixed-size batches to the P2P network.
func (h *StorageHandler) StoreRaptorQSymbolsIntoP2P(ctx context.Context, taskID, symbolsDir string) error {
	/* record directory in DB */
	if err := h.store.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
		return fmt.Errorf("store symbol dir: %w", err)
	}

	/* gather every symbol path under symbolsDir ------------------------- */
	keys, err := walkSymbolTree(symbolsDir)
	if err != nil {
		return err
	}

	/* down-sample if we exceed the “big directory” threshold ------------- */
	if len(keys) > loadSymbolsBatchSize {
		want := int(math.Ceil(float64(len(keys)) * storeSymbolsPercent / 100))
		if want < len(keys) {
			rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			keys = keys[:want]
		}
		sort.Strings(keys) // deterministic order inside the sample
	}

	log.WithContext(ctx).WithField("count", len(keys)).Info("storing RaptorQ symbols")

	/* stream in fixed-size batches -------------------------------------- */
	for start := 0; start < len(keys); {
		end := start + loadSymbolsBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		if err := h.storeSymbolsInP2P(ctx, taskID, symbolsDir, keys[start:end]); err != nil {
			return err
		}
		start = end
	}

	if err := h.store.UpdateIsFirstBatchStored(h.TxID); err != nil {
		return fmt.Errorf("update first-batch flag: %w", err)
	}
	log.WithContext(ctx).WithField("curr-time", time.Now().UTC()).WithField("count", len(keys)).
		Info("finished storing RaptorQ symbols")

	return nil
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

func (h *StorageHandler) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) error {
	log.WithContext(ctx).WithField("count", len(fileKeys)).Info("loading batch symbols")

	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return fmt.Errorf("load symbols: %w", err)
	}

	if err := h.P2PClient.StoreBatch(ctx, symbols, P2PDataRaptorQSymbol, taskID); err != nil {
		return fmt.Errorf("p2p store batch: %w", err)
	}
	log.WithContext(ctx).WithField("count", len(symbols)).Info("stored batch symbols")

	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	log.WithContext(ctx).WithField("count", len(symbols)).Info("deleted batch symbols")

	return nil
}
