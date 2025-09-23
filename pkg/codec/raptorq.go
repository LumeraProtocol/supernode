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

const (
	rqSymbolSize       uint16 = 65535
	rqRedundancyFactor uint8  = 6
	// Limit RaptorQ processor memory usage to ~2 GiB
	rqMaxMemoryMB uint64 = 2 * 1024 // MB
	// Concurrency tuned for 2 GiB limit and typical 8+ core CPUs
	rqConcurrency uint64 = 1
	// Target single-block output for up to 1 GiB files with padding headroom (~1.25 GiB)
	rqBlockSize int = 1280 * 1024 * 1024 // bytes (1,280 MiB)
)

type raptorQ struct {
	symbolsBaseDir string
}

func NewRaptorQCodec(dir string) Codec {
	return &raptorQ{
		symbolsBaseDir: dir,
	}

}

func (rq *raptorQ) Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error) {
	/* ---------- 1.  initialise RaptorQ processor ---------- */
	fields := logtrace.Fields{
		logtrace.FieldMethod: "Encode",
		logtrace.FieldModule: "rq",
		logtrace.FieldTaskID: req.TaskID,
		"path":               req.Path,
		"data-size":          req.DataSize,
	}

	processor, err := raptorq.NewRaptorQProcessor(rqSymbolSize, rqRedundancyFactor, rqMaxMemoryMB, rqConcurrency)
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()
	logtrace.Info(ctx, "RaptorQ processor created", fields)

	/* ---------- 1.  run the encoder ---------- */
	// Deterministic: force single block
	blockSize := rqBlockSize

	symbolsDir := filepath.Join(rq.symbolsBaseDir, req.TaskID)
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		fields[logtrace.FieldError] = err.Error()
		os.Remove(req.Path)
		return EncodeResponse{}, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}
	logtrace.Info(ctx, "RaptorQ processor encoding", fields)

	resp, err := processor.EncodeFile(req.Path, symbolsDir, blockSize)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		os.Remove(req.Path)
		return EncodeResponse{}, fmt.Errorf("raptorq encode: %w", err)
	}

	/* we no longer need the temp file */
	// _ = os.Remove(tmpPath)

	/* ---------- 2.  read the layout JSON ---------- */
	layoutData, err := os.ReadFile(resp.LayoutFilePath)
	logtrace.Info(ctx, "RaptorQ processor layout file", logtrace.Fields{
		"layout-file": resp.LayoutFilePath})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return EncodeResponse{}, fmt.Errorf("read layout %s: %w", resp.LayoutFilePath, err)
	}

	var encodeResp EncodeResponse
	if err := json.Unmarshal(layoutData, &encodeResp.Metadata); err != nil {
		return EncodeResponse{}, fmt.Errorf("unmarshal layout: %w", err)
	}
	encodeResp.SymbolsDir = symbolsDir

	// Enforce single-block output; abort if multiple blocks are produced
	if n := len(encodeResp.Metadata.Blocks); n != 1 {
		return EncodeResponse{}, fmt.Errorf("raptorq encode produced %d blocks; single-block layout is required", n)
	}

	return encodeResp, nil
}
