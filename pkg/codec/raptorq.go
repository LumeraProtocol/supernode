package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// Fixed policy (linux/amd64 only):
// - Concurrency: 1
// - Symbol size: 65535
// - Redundancy: 5
// - Max memory: use detected system/cgroup memory with headroom
const (
	fixedConcurrency  = 1
	defaultRedundancy = 5
	headroomPct       = 20  // simple fixed safety margin
	targetBlockMB     = 128 // cap block size on encode (MB); 0 means use recommended
)

type raptorQ struct {
	symbolsBaseDir string
}

func NewRaptorQCodec(dir string) Codec {
	return &raptorQ{
		symbolsBaseDir: dir,
	}

}

// newProcessor constructs a RaptorQ processor using a fixed policy:
// - concurrency=1
// - symbol size=65535
// - redundancy=5
// - max memory = detected system/cgroup memory with slight headroom
func newProcessor(ctx context.Context) (*raptorq.RaptorQProcessor, error) {
	memLimitMB, memSource := detectMemoryLimitMB()
	usableMemMB := computeUsableMem(memLimitMB, headroomPct)

	// Fixed params
	symbolSize := uint16(raptorq.DefaultSymbolSize)
	redundancy := uint8(defaultRedundancy)
	concurrency := uint64(fixedConcurrency)
	maxMemMB := usableMemMB

	perWorkerMB := uint64(0)
	if concurrency > 0 {
		perWorkerMB = maxMemMB / concurrency
	}
	logtrace.Info(ctx, "RaptorQ processor config", logtrace.Fields{
		"symbol_size":       symbolSize,
		"redundancy_factor": redundancy,
		"max_memory_mb":     maxMemMB,
		"concurrency":       concurrency,
		"per_worker_mb":     perWorkerMB,
		"headroom_pct":      headroomPct,
		"mem_limit_mb":      memLimitMB,
		"mem_limit_source":  memSource,
	})

	return raptorq.NewRaptorQProcessor(symbolSize, redundancy, maxMemMB, concurrency)
}

// detectMemoryLimitMB determines the memory limit (MB) from cgroups, falling back to MemTotal.
func detectMemoryLimitMB() (uint64, string) {
	// cgroup v2: /sys/fs/cgroup/memory.max
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		s := strings.TrimSpace(string(b))
		if s != "max" {
			if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
				return v / (1024 * 1024), "cgroupv2:memory.max"
			}
		}
	}
	// cgroup v1: /sys/fs/cgroup/memory/memory.limit_in_bytes
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		s := strings.TrimSpace(string(b))
		if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
			// Some systems report a huge number when unlimited; treat > 1PB as unlimited
			if v <= 1<<50 {
				return v / (1024 * 1024), "cgroupv1:memory.limit_in_bytes"
			}
			// unlimited; fallthrough to MemTotal
		}
	}
	// Fallback: /proc/meminfo MemTotal
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(b), "\n")
		for _, ln := range lines {
			if strings.HasPrefix(ln, "MemTotal:") {
				f := strings.Fields(ln)
				if len(f) >= 2 {
					if kb, err := strconv.ParseUint(f[1], 10, 64); err == nil {
						return kb / 1024, "meminfo:MemTotal"
					}
				}
			}
		}
	}
	return 0, "unknown"
}

// computeUsableMem applies a headroom percentage to a memory limit using integer math.
func computeUsableMem(memLimitMB uint64, headroomPct int) uint64 {
	if headroomPct <= 0 || memLimitMB == 0 {
		return memLimitMB
	}
	return memLimitMB - (memLimitMB*uint64(headroomPct))/100
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

	// Create processor using fixed policy (linux/amd64, no env)
	processor, err := newProcessor(ctx)
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()
	logtrace.Info(ctx, "RaptorQ processor created", fields)

	/* ---------- 1.  run the encoder ---------- */
	// Determine block size with a simple cap (no env): use min(recommended, targetBlockMB)
	rec := processor.GetRecommendedBlockSize(uint64(req.DataSize))
	var blockSize int
	if targetBlockMB > 0 {
		targetBytes := int(targetBlockMB) * 1024 * 1024
		if rec == 0 || rec > targetBytes {
			blockSize = targetBytes
		} else {
			blockSize = rec
		}
	} else {
		blockSize = rec
	}
	logtrace.Info(ctx, "RaptorQ recommended block size", logtrace.Fields{"block_size": rec, "chosen_block_size": blockSize, "target_block_mb": targetBlockMB})

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

	// layout will be read from disk below

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
