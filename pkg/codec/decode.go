package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

type DecodeRequest struct {
	ActionID string
	Layout   Layout
	Symbols  map[string][]byte
}

type DecodeResponse struct {
	FilePath     string
	DecodeTmpDir string
}

// Workspace holds paths & reverse index for prepared decoding.
type Workspace struct {
	ActionID      string
	SymbolsDir    string   // .../<base>/<actionID>
	BlockDirs     []string // index = blockID (or 0 if single block)
	symbolToBlock map[string]int
	mu            sync.RWMutex // protects symbolToBlock reads if you expand it later
}

// PrepareDecode creates the on-disk workspace for decoding and returns:
//   - blockPaths[0] => where to write symbols for block 0 (your single-block case)
//   - Write(block, id, data) callback that writes symbol bytes directly to disk
//   - Cleanup() to remove the workspace on abort (no-op if you want to keep it)
func (rq *raptorQ) PrepareDecode(
	ctx context.Context,
	actionID string,
	layout Layout,
) (blockPaths []string, Write func(block int, symbolID string, data []byte) (string, error), Cleanup func() error, ws *Workspace, err error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "PrepareDecode",
		logtrace.FieldModule:   "rq",
		logtrace.FieldActionID: actionID,
	}

	// Create root symbols dir for this action
	symbolsDir := filepath.Join(rq.symbolsBaseDir, actionID)
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "mkdir symbols base dir failed", fields)
		return nil, nil, nil, nil, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}

	// Ensure block directories exist; build reverse index symbol -> block
	maxBlockID := 0
	for _, b := range layout.Blocks {
		if int(b.BlockID) > maxBlockID {
			maxBlockID = int(b.BlockID)
		}
	}
	blockDirs := make([]string, maxBlockID+1)
	s2b := make(map[string]int, 1024)

	for _, b := range layout.Blocks {
		dir := filepath.Join(symbolsDir, fmt.Sprintf("block_%d", b.BlockID))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fields[logtrace.FieldError] = err.Error()
			logtrace.Error(ctx, "mkdir block dir failed", fields)
			return nil, nil, nil, nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
		blockDirs[b.BlockID] = dir
		for _, sym := range b.Symbols {
			s2b[sym] = b.BlockID
		}
	}

	ws = &Workspace{
		ActionID:      actionID,
		SymbolsDir:    symbolsDir,
		BlockDirs:     blockDirs,
		symbolToBlock: s2b,
	}

	// Helper: atomic write (tmp file + rename) to avoid partials on crash
	writeFileAtomic := func(path string, data []byte) error {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}

	// Write callback; if block < 0, resolve via layout reverse index; default to 0.
	Write = func(block int, symbolID string, data []byte) (string, error) {
		// Quick cancellation check
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Resolve block if caller passes default
		if block < 0 {
			ws.mu.RLock()
			bid, ok := ws.symbolToBlock[symbolID]
			ws.mu.RUnlock()
			if !ok {
				// Single-block simplification: default to block 0 if layout maps are absent
				if len(ws.BlockDirs) == 0 || ws.BlockDirs[0] == "" {
					return "", errors.Errorf("no block directories prepared")
				}
				bid = 0
			}
			block = bid
		}

		if block < 0 || block >= len(ws.BlockDirs) || ws.BlockDirs[block] == "" {
			return "", errors.Errorf("invalid block index %d", block)
		}

		// sanitize symbolID to a basename (prevents traversal)
		clean := path.Clean("/" + symbolID)
		base := strings.TrimPrefix(clean, "/")
		if base == "" || strings.Contains(base, "/") {
			return "", errors.Errorf("invalid symbol id %q", symbolID)

		}

		dest := filepath.Join(ws.BlockDirs[block], base)
		if err := writeFileAtomic(dest, data); err != nil {
			return "", fmt.Errorf("write symbol %s (block %d): %w", base, block, err)
		}
		return dest, nil
	}

	Cleanup = func() error {
		// Remove the whole workspace directory (symbols + layout + output if any)
		return os.RemoveAll(symbolsDir)
	}

	logtrace.Info(ctx, "prepare decode workspace created", logtrace.Fields{
		"symbols_dir": symbolsDir,
		"blocks":      len(blockDirs),
	})
	return blockDirs, Write, Cleanup, ws, nil
}

// DecodeFromPrepared performs RaptorQ decode using an already-prepared workspace.
// It writes layout.json under the workspace, runs decode, and returns output paths.
func (rq *raptorQ) DecodeFromPrepared(
	ctx context.Context,
	ws *Workspace,
	layout Layout,
) (DecodeResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "DecodeFromPrepared",
		logtrace.FieldModule:   "rq",
		logtrace.FieldActionID: ws.ActionID,
	}
	logtrace.Info(ctx, "RaptorQ decode (prepared) requested", fields)

	processor, err := raptorq.NewRaptorQProcessor(rqSymbolSize, rqRedundancyFactor, rqMaxMemoryMB, rqConcurrency)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()

	// Write layout.json (idempotent)
	layoutPath := filepath.Join(ws.SymbolsDir, "layout.json")
	layoutBytes, err := json.Marshal(layout)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("marshal layout: %w", err)
	}
	if err := os.WriteFile(layoutPath, layoutBytes, 0o644); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("write layout file: %w", err)
	}
	logtrace.Info(ctx, "layout.json written (prepared)", fields)

	// Decode to output (idempotent-safe: overwrite on success)
	outputPath := filepath.Join(ws.SymbolsDir, "output")
	if err := processor.DecodeSymbols(ws.SymbolsDir, outputPath, layoutPath); err != nil {
		fields[logtrace.FieldError] = err.Error()
		_ = os.Remove(outputPath) // best-effort cleanup of partial output
		return DecodeResponse{}, fmt.Errorf("raptorq decode: %w", err)
	}

	logtrace.Info(ctx, "RaptorQ decoding completed successfully (prepared)", logtrace.Fields{
		"output_path": outputPath,
	})
	return DecodeResponse{FilePath: outputPath, DecodeTmpDir: ws.SymbolsDir}, nil
}

func (rq *raptorQ) Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error) {
    fields := logtrace.Fields{
        logtrace.FieldMethod:   "Decode",
        logtrace.FieldModule:   "rq",
        logtrace.FieldActionID: req.ActionID,
    }
    logtrace.Info(ctx, "RaptorQ decode request received", fields)

    // 1) Validate layout (the check)
    if len(req.Layout.Blocks) == 0 {
        fields[logtrace.FieldError] = "empty layout"
        return DecodeResponse{}, fmt.Errorf("invalid layout: no blocks present")
    }
    for _, blk := range req.Layout.Blocks {
        if len(blk.Symbols) == 0 {
            fields[logtrace.FieldError] = fmt.Sprintf("block_%d has no symbols", blk.BlockID)
            return DecodeResponse{}, fmt.Errorf("invalid layout: block %d has no symbols", blk.BlockID)
        }
    }

    // 2) Prepare workspace (functionality)
    _, Write, Cleanup, ws, err := rq.PrepareDecode(ctx, req.ActionID, req.Layout)
    if err != nil {
        fields[logtrace.FieldError] = err.Error()
        return DecodeResponse{}, fmt.Errorf("prepare decode workspace: %w", err)
    }

    // Ensure workspace cleanup on failure. On success, caller cleans up via returned path.
    success := false
    defer func() {
        if !success && Cleanup != nil {
            _ = Cleanup()
        }
    }()

    // 3) Persist provided in-memory symbols via Write (functionality)
    if len(req.Symbols) > 0 {
        for id, data := range req.Symbols {
            if _, werr := Write(-1, id, data); werr != nil {
                fields[logtrace.FieldError] = werr.Error()
                return DecodeResponse{}, werr
            }
        }
        logtrace.Info(ctx, "symbols persisted via Write()", fields)
    }

    // 4) Decode using the prepared workspace (functionality)
    resp, derr := rq.DecodeFromPrepared(ctx, ws, req.Layout)
    if derr != nil {
        fields[logtrace.FieldError] = derr.Error()
        return DecodeResponse{}, derr
    }
    success = true
    return resp, nil
}
