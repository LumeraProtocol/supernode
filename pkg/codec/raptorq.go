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
	logtrace.Debug(ctx, "RaptorQ processor created", fields)

	/* ---------- 1.  run the encoder ---------- */
	// Deterministic: force single block
	blockSize := rqBlockSize

	symbolsDir := filepath.Join(rq.symbolsBaseDir, req.TaskID)
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		fields[logtrace.FieldError] = err.Error()
		return EncodeResponse{}, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}
	logtrace.Debug(ctx, "RaptorQ processor encoding", fields)

	resp, err := processor.EncodeFile(req.Path, symbolsDir, blockSize)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return EncodeResponse{}, fmt.Errorf("raptorq encode: %w", err)
	}

	/* ---------- 2.  read the layout JSON ---------- */
	layoutData, err := os.ReadFile(resp.LayoutFilePath)
	logtrace.Debug(ctx, "RaptorQ processor layout file", logtrace.Fields{
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

// CreateMetadata builds only the layout metadata for the given file without generating symbols.
func (rq *raptorQ) CreateMetadata(ctx context.Context, path string) (Layout, error) {
	// Populate fields; include data-size by stat-ing the file to preserve existing log fields
	fields := logtrace.Fields{
		logtrace.FieldMethod: "CreateMetadata",
		logtrace.FieldModule: "rq",
		"path":               path,
	}
	if fi, err := os.Stat(path); err == nil {
		fields["data-size"] = int(fi.Size())
	}

	processor, err := raptorq.NewRaptorQProcessor(rqSymbolSize, rqRedundancyFactor, rqMaxMemoryMB, rqConcurrency)
	if err != nil {
		return Layout{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()
	logtrace.Debug(ctx, "RaptorQ processor created", fields)

	// Deterministic: force single block
	blockSize := rqBlockSize

	// Prepare a temporary path for the generated layout file
	base := rq.symbolsBaseDir
	if base == "" {
		base = os.TempDir()
	}
	tmpDir, err := os.MkdirTemp(base, "rq_meta_*")
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return Layout{}, fmt.Errorf("mkdir temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	layoutPath := filepath.Join(tmpDir, "layout.json")

	// Use rq-go's metadata-only creation; no symbols are produced here.
	resp, err := processor.CreateMetadata(path, layoutPath, blockSize)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return Layout{}, fmt.Errorf("raptorq create metadata: %w", err)
	}

	layoutData, err := os.ReadFile(resp.LayoutFilePath)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		return Layout{}, fmt.Errorf("read layout %s: %w", resp.LayoutFilePath, err)
	}

	var layout Layout
	if err := json.Unmarshal(layoutData, &layout); err != nil {
		return Layout{}, fmt.Errorf("unmarshal layout: %w", err)
	}

	// Enforce single-block output; abort if multiple blocks are produced
	if n := len(layout.Blocks); n != 1 {
		return Layout{}, fmt.Errorf("raptorq metadata produced %d blocks; single-block layout is required", n)
	}

	return layout, nil
}
