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

    // Write symbols to disk as soon as possible to reduce heap residency.
    // Prior versions kept symbols in memory until after decode, which could
    // exacerbate memory pressure. We now persist to disk immediately and drop each entry
    // from the map to allow GC to reclaim memory sooner. This helps avoid spikes that
    // could coincide with RaptorQ allocations (cf. memory limit exceeded reports).
    for id, data := range req.Symbols {
        symbolPath := filepath.Join(symbolsDir, id)
        if err := os.WriteFile(symbolPath, data, 0o644); err != nil {
            fields[logtrace.FieldError] = err.Error()
            return DecodeResponse{}, fmt.Errorf("write symbol %s: %w", id, err)
        }
        // Drop the in-memory copy promptly; safe to delete during range
        delete(req.Symbols, id)
    }
    logtrace.Info(ctx, "symbols written to disk", fields)

    // ---------- write layout file ----------
    // Use a conventional filename to match rq-go documentation examples.
    // The library consumes the explicit path, so name is not strictly required, but
    // aligning with `_raptorq_layout.json` aids operators/debuggers.
    layoutPath := filepath.Join(symbolsDir, "_raptorq_layout.json")
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
