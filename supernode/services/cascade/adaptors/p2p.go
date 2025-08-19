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
	loadSymbolsBatchSize = 2500
	storeSymbolsPercent  = 10
)

// P2PService defines the interface for storing data in the P2P layer.
//
//go:generate mockgen -destination=mocks/p2p_mock.go -package=cascadeadaptormocks -source=p2p.go
type P2PService interface {
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
	logtrace.Info(ctx, "About to store ID files", logtrace.Fields{"taskID": req.TaskID, "fileCount": len(req.IDFiles)})

	if err := p.storeCascadeMetadata(ctx, req.IDFiles, req.TaskID); err != nil {
		return errors.Wrap(err, "failed to store ID files")
	}
	logtrace.Info(ctx, "id files have been stored", f)

	if err := p.storeCascadeSymbols(ctx, req.TaskID, req.ActionID, req.SymbolsDir); err != nil {
		return errors.Wrap(err, "error storing raptor-q symbols")
	}
	logtrace.Info(ctx, "raptor-q symbols have been stored", f)

	return nil
}

func (p *p2pImpl) storeCascadeMetadata(ctx context.Context, metadataFiles [][]byte, taskID string) error {
	logtrace.Info(ctx, "Storing cascade metadata", logtrace.Fields{
		"taskID":    taskID,
		"fileCount": len(metadataFiles),
	})

	return p.p2p.StoreBatch(ctx, metadataFiles, storage.P2PDataCascadeMetadata, taskID)
}

func (p *p2pImpl) storeCascadeSymbols(ctx context.Context, taskID, actionID string, symbolsDir string) error {
	/* record directory in DB */
	if err := p.rqStore.StoreSymbolDirectory(taskID, symbolsDir); err != nil {
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

	logtrace.Info(ctx, "storing RaptorQ symbols", logtrace.Fields{"count": len(keys)})

	/* stream in fixed-size batches -------------------------------------- */
	for start := 0; start < len(keys); {
		end := start + loadSymbolsBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		if err := p.storeSymbolsInP2P(ctx, taskID, symbolsDir, keys[start:end]); err != nil {
			return err
		}
		start = end
	}

	if err := p.rqStore.UpdateIsFirstBatchStored(actionID); err != nil {
		return fmt.Errorf("update first-batch flag: %w", err)
	}
	logtrace.Info(ctx, "finished storing RaptorQ symbols", logtrace.Fields{
		"curr-time": time.Now().UTC(),
		"count":     len(keys),
	})

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

func (c *p2pImpl) storeSymbolsInP2P(ctx context.Context, taskID, root string, fileKeys []string) error {
	logtrace.Info(ctx, "loading batch symbols", logtrace.Fields{"count": len(fileKeys)})

	symbols, err := utils.LoadSymbols(root, fileKeys)
	if err != nil {
		return fmt.Errorf("load symbols: %w", err)
	}

	if err := c.p2p.StoreBatch(ctx, symbols, storage.P2PDataRaptorQSymbol, taskID); err != nil {
		return fmt.Errorf("p2p store batch: %w", err)
	}
	logtrace.Info(ctx, "stored batch symbols", logtrace.Fields{"count": len(symbols)})

	if err := utils.DeleteSymbols(ctx, root, fileKeys); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}
	logtrace.Info(ctx, "deleted batch symbols", logtrace.Fields{"count": len(symbols)})

	return nil
}
