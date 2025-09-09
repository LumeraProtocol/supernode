package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

type DecodeRequest struct {
	ActionID string
	Layout   Layout
	Symbols  map[string][]byte
}

type DecodeResponse struct {
	Path         string
	DecodeTmpDir string
}

func (rq *raptorQ) Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "Decode",
		logtrace.FieldModule:   "rq",
		logtrace.FieldActionID: req.ActionID,
	}
	logtrace.Info(ctx, "RaptorQ decode request received", fields)

	// Create processor using fixed policy (no env overrides)
	processor, err := newProcessor(ctx)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()

	symbolsDir := filepath.Join(rq.symbolsBaseDir, req.ActionID)
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}

	// Build a reverse index from symbol ID -> block ID from the provided layout
	symToBlock := buildSymbolToBlockIndex(req.Layout)

	// Write symbols to disk promptly to reduce heap residency. Place them under
	// symbolsDir/block_<block_id>/<symbolID> to match rq-go expectations.
	if err := writeSymbolsPerBlock(ctx, symbolsDir, symToBlock, req.Symbols); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, err
	}
	logtrace.Info(ctx, "symbols written to disk (per-block)", fields)

	// ---------- write layout file ----------
	// Use a conventional filename to match rq-go documentation examples.
	// The library consumes the explicit path, so name is not strictly required, but
	// aligning with `_raptorq_layout.json` aids operators/debuggers.
	layoutPath, err := writeLayoutFile(req.Layout, symbolsDir)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, err
	}
	logtrace.Info(ctx, "layout.json written", fields)

	// ---------- preflight check: ensure at least one symbol exists for each block ----------
	perBlockCounts := computePerBlockCounts(req.Layout, symbolsDir)
	fields["per_block_counts"] = perBlockCounts
	logtrace.Info(ctx, "pre-decode per-block symbol counts", fields)

	// If any block has zero symbols, fail fast with an "insufficient symbols" message
	// so the progressive retriever can escalate and fetch more.
	for _, blk := range req.Layout.Blocks {
		if perBlockCounts[blk.BlockID] == 0 {
			return DecodeResponse{}, fmt.Errorf("insufficient symbols: no symbols found for block %d", blk.BlockID)
		}
	}

	// Decode the symbols into an output file using the provided layout.
	outputPath := filepath.Join(symbolsDir, "output")
	if err := processor.DecodeSymbols(symbolsDir, outputPath, layoutPath); err != nil {
		fields[logtrace.FieldError] = err.Error()
		_ = os.Remove(outputPath)
		return DecodeResponse{}, fmt.Errorf("raptorq decode: %w", err)
	}

	logtrace.Info(ctx, "RaptorQ decoding completed successfully", fields)
	return DecodeResponse{Path: outputPath, DecodeTmpDir: symbolsDir}, nil
}

// buildSymbolToBlockIndex constructs a lookup from symbol ID to its block ID
// based on the provided layout.
func buildSymbolToBlockIndex(layout Layout) map[string]int {
	m := make(map[string]int)
	for _, blk := range layout.Blocks {
		for _, sid := range blk.Symbols {
			m[sid] = blk.BlockID
		}
	}
	return m
}

// writeSymbolsPerBlock writes symbols to per-block directories to match rq-go expectations.
// It also deletes each symbol from the provided map after persisting to reduce heap residency.
func writeSymbolsPerBlock(ctx context.Context, symbolsDir string, symToBlock map[string]int, symbols map[string][]byte) error {
	for id, data := range symbols {
		blockID, ok := symToBlock[id]
		var destDir string
		if ok {
			destDir = filepath.Join(symbolsDir, fmt.Sprintf("block_%d", blockID))
		} else {
			// Fallback: if symbol not present in layout (unexpected), keep at root.
			destDir = symbolsDir
			logtrace.Info(ctx, "symbol ID not present in layout; writing at root", logtrace.Fields{"symbol_id": id})
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", destDir, err)
		}
		symbolPath := filepath.Join(destDir, id)
		if err := os.WriteFile(symbolPath, data, 0o644); err != nil {
			return fmt.Errorf("write symbol %s: %w", id, err)
		}
		delete(symbols, id)
	}
	return nil
}

// writeLayoutFile marshals and writes the layout JSON into the symbols directory.
func writeLayoutFile(layout Layout, symbolsDir string) (string, error) {
	layoutPath := filepath.Join(symbolsDir, "_raptorq_layout.json")
	layoutBytes, err := json.Marshal(layout)
	if err != nil {
		return "", fmt.Errorf("marshal layout: %w", err)
	}
	if err := os.WriteFile(layoutPath, layoutBytes, 0o644); err != nil {
		return "", fmt.Errorf("write layout file: %w", err)
	}
	return layoutPath, nil
}

// computePerBlockCounts counts non-directory, non-layout files under each block directory.
func computePerBlockCounts(layout Layout, symbolsDir string) map[int]int {
	counts := make(map[int]int)
	for _, blk := range layout.Blocks {
		blockDir := filepath.Join(symbolsDir, fmt.Sprintf("block_%d", blk.BlockID))
		entries, err := os.ReadDir(blockDir)
		if err != nil {
			counts[blk.BlockID] = 0
			continue
		}
		c := 0
		for _, e := range entries {
			if e.IsDir() || e.Name() == "_raptorq_layout.json" {
				continue
			}
			c++
		}
		counts[blk.BlockID] = c
	}
	return counts
}
