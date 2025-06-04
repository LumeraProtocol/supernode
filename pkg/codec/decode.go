package codec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

type DecodeRequest struct {
	ActionID string
	Layout   Layout
	Symbols  map[string][]byte
}

type DecodeResponse struct {
	Path       string
	LayoutPath string
}

func (rq *raptorQ) Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:   "Decode",
		logtrace.FieldModule:   "rq",
		logtrace.FieldActionID: req.ActionID,
	}
	logtrace.Info(ctx, "RaptorQ decode request received", fields)

	processor, err := raptorq.NewDefaultRaptorQProcessor()
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

	// Write symbols to disk
	for id, data := range req.Symbols {
		symbolPath := filepath.Join(symbolsDir, id)
		if err := os.WriteFile(symbolPath, data, 0o644); err != nil {
			fields[logtrace.FieldError] = err.Error()
			return DecodeResponse{}, fmt.Errorf("write symbol %s: %w", id, err)
		}
	}
	logtrace.Info(ctx, "symbols written to disk", fields)

	// Decode
	outputPath := filepath.Join(symbolsDir, "output")
	if err := processor.DecodeSymbols(symbolsDir, outputPath, ""); err != nil {
		fields[logtrace.FieldError] = err.Error()
		_ = os.Remove(outputPath)
		return DecodeResponse{}, fmt.Errorf("raptorq decode: %w", err)
	}

	logtrace.Info(ctx, "RaptorQ decoding completed successfully", fields)
	return DecodeResponse{Path: outputPath, LayoutPath: ""}, nil
}
