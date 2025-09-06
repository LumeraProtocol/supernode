package codec

import (
    "context"
    "encoding/json"
    "fmt"
    "math"
    "os"
    "path/filepath"
    "strconv"
    "runtime"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

type raptorQ struct {
	symbolsBaseDir string
}

func NewRaptorQCodec(dir string) Codec {
	return &raptorQ{
		symbolsBaseDir: dir,
	}

}

// newProcessor constructs a RaptorQ processor using environment overrides when provided.
//
// Environment variables:
// - LUMERA_RQ_SYMBOL_SIZE      (uint16, default 65535)
// - LUMERA_RQ_REDUNDANCY       (uint8,  default 5)
// - LUMERA_RQ_MAX_MEMORY_MB    (uint64, default 16384)
// - LUMERA_RQ_CONCURRENCY      (uint64, default 4)
//
// The goal is to allow deployments to tune memory and concurrency to avoid pressure spikes
// without changing code paths, while keeping the prior defaults when unset.
func newProcessor(ctx context.Context) (*raptorq.RaptorQProcessor, error) {
	// Helper to read env with fallback and bounds
	readUint := func(env string, def uint64, min uint64, max uint64) uint64 {
		if v, ok := os.LookupEnv(env); ok {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				if n < min {
					return min
				}
				if max > 0 {
					return uint64(math.Min(float64(n), float64(max)))
				}
				return n
			}
		}
		return def
	}

    // Recommended defaults for <= 1GB artefacts with performance padding.
    // Prior default MaxMemory was 16GB and concurrency fixed at 4.
    // For 1GB max files, a lower cap (e.g., 6–8GB) with concurrency up to CPU cores
    // balances throughput with memory headroom. These are only defaults — env vars
    // still override them when set.
    recommendedMaxMem := uint64(8 * 1024) // 8 GB default cap (tunable via env)
    cpu := runtime.NumCPU()
    if cpu < 1 {
        cpu = 1
    }
    recommendedConcurrency := uint64(cpu)
    if recommendedConcurrency > 8 {
        recommendedConcurrency = 8 // avoid oversubscription on very large hosts
    }

    // Read overrides (with recommended defaults)
    symbolSize := uint16(readUint("LUMERA_RQ_SYMBOL_SIZE", uint64(raptorq.DefaultSymbolSize), 1024, 65535))
    // Default redundancy raised to 5 for better repair tolerance on ≤1 GB files.
    // Env var still overrides if set.
    redundancy := uint8(readUint("LUMERA_RQ_REDUNDANCY", 5, 1, 32))
    maxMemMB := readUint("LUMERA_RQ_MAX_MEMORY_MB", recommendedMaxMem, 256, 1<<20)
    concurrency := readUint("LUMERA_RQ_CONCURRENCY", recommendedConcurrency, 1, 1024)

	// Emit chosen configuration for traceability
	logtrace.Info(ctx, "RaptorQ processor config", logtrace.Fields{
		"symbol_size":       symbolSize,
		"redundancy_factor": redundancy,
		"max_memory_mb":     maxMemMB,
		"concurrency":       concurrency,
	})

	return raptorq.NewRaptorQProcessor(symbolSize, redundancy, maxMemMB, concurrency)
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

	// Use env-configurable processor to allow memory/concurrency tuning per deployment
	processor, err := newProcessor(ctx)
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()
	logtrace.Info(ctx, "RaptorQ processor created", fields)

	/* ---------- 1.  run the encoder ---------- */
	blockSize := processor.GetRecommendedBlockSize(uint64(req.DataSize))

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

	return encodeResp, nil
}
