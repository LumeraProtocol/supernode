package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	raptorq "github.com/LumeraProtocol/rq-go"
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

	// Use deterministic processor settings (matching encoder)
	processor, err := raptorq.NewRaptorQProcessor(rqSymbolSize, rqRedundancyFactor, rqMaxMemoryMB, rqConcurrency)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()

	symbolsDir := filepath.Join(rq.symbolsBaseDir, req.ActionID)
	// Ensure a clean scratch directory (avoid contamination from previous attempts)
	if err := os.RemoveAll(symbolsDir); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("cleanup decode dir %s: %w", symbolsDir, err)
	}
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}

	// Validate layout before writing any symbols
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

	// Build symbol->block mapping from layout and ensure block directories exist
	symbolToBlock := make(map[string]int)
	for _, blk := range req.Layout.Blocks {
		blockDir := filepath.Join(symbolsDir, fmt.Sprintf("block_%d", blk.BlockID))
		if err := os.MkdirAll(blockDir, 0o755); err != nil {
			fields[logtrace.FieldError] = err.Error()
			return DecodeResponse{}, fmt.Errorf("mkdir %s: %w", blockDir, err)
		}
		for _, sym := range blk.Symbols {
			symbolToBlock[sym] = blk.BlockID
		}
	}

	// Write symbols to their respective block directories
	for id, data := range req.Symbols {
		blkID, ok := symbolToBlock[id]
		if !ok {
			fields[logtrace.FieldError] = "symbol not present in layout"
			return DecodeResponse{}, fmt.Errorf("symbol %s not present in layout", id)
		}
		blockDir := filepath.Join(symbolsDir, fmt.Sprintf("block_%d", blkID))
		symbolPath := filepath.Join(blockDir, id)
		if err := os.WriteFile(symbolPath, data, 0o644); err != nil {
			fields[logtrace.FieldError] = err.Error()
			return DecodeResponse{}, fmt.Errorf("write symbol %s: %w", id, err)
		}
	}
	logtrace.Info(ctx, "symbols written to block directories", fields)

	// ---------- write layout.json ----------
	layoutPath := filepath.Join(symbolsDir, "layout.json")
	layoutBytes, err := json.Marshal(req.Layout)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("marshal layout: %w", err)
	}
	if err := os.WriteFile(layoutPath, layoutBytes, 0o644); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return DecodeResponse{}, fmt.Errorf("write layout file: %w", err)
	}
	logtrace.Info(ctx, "layout.json written", fields)

	// Decode
	outputPath := filepath.Join(symbolsDir, "output")
	if err := processor.DecodeSymbols(symbolsDir, outputPath, layoutPath); err != nil {
		fields[logtrace.FieldError] = err.Error()
		_ = os.Remove(outputPath)
		return DecodeResponse{}, fmt.Errorf("raptorq decode: %w", err)
	}

	logtrace.Info(ctx, "RaptorQ decoding completed successfully", fields)
	return DecodeResponse{Path: outputPath, DecodeTmpDir: symbolsDir}, nil
}
