package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

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
	// 1) Detect resources (memory/CPU)
	memLimitMB, memSource := detectMemoryLimitMB()
	effCores, cpuSource := detectEffectiveCores()

	// 2) Apply headroom knob and compute usable memory
	headroomPct := readInt("LUMERA_RQ_MEM_HEADROOM_PCT", 40, 0, 90)
	usableMemMB := computeUsableMem(memLimitMB, headroomPct)

	// 3) Select profile (forced via env or inferred)
	profile := selectProfile(memLimitMB, os.Getenv("CODEC_PROFILE"))

	// 4) Compute default limits for the chosen profile
	defMaxMemMB, defConcurrency := defaultLimitsForProfile(profile, usableMemMB)

	// 5) Adjust concurrency by effective cores/quotas
	defConcurrency = adjustConcurrency(defConcurrency, effCores)

	// 6) Ensure per-worker memory target (>=512MB) by reducing concurrency if needed
	defConcurrency = rebalancePerWorkerMem(defMaxMemMB, defConcurrency, 512)

	// 7) Read env overrides (env wins)
	symbolSize := uint16(readUint("LUMERA_RQ_SYMBOL_SIZE", uint64(raptorq.DefaultSymbolSize), 1024, 65535))
	redundancy := uint8(readUint("LUMERA_RQ_REDUNDANCY", 5, 1, 32))
	maxMemMB := readUint("LUMERA_RQ_MAX_MEMORY_MB", defMaxMemMB, 256, 1<<20)
	concurrency := readUint("LUMERA_RQ_CONCURRENCY", defConcurrency, 1, 1024)

	// 8) Log final configuration
	logtrace.Info(ctx, "RaptorQ processor config", logtrace.Fields{
		"symbol_size":       symbolSize,
		"redundancy_factor": redundancy,
		"max_memory_mb":     maxMemMB,
		"concurrency":       concurrency,
		"profile":           profile,
		"headroom_pct":      headroomPct,
		"mem_limit_mb":      memLimitMB,
		"mem_limit_source":  memSource,
		"effective_cores":   effCores,
		"cpu_limit_source":  cpuSource,
	})

	// 9) Construct processor
	return raptorq.NewRaptorQProcessor(symbolSize, redundancy, maxMemMB, concurrency)
}

// RaptorQConfig describes the effective codec configuration derived from env, resources and defaults.
type RaptorQConfig struct {
    SymbolSize      uint16
    Redundancy      uint8
    MaxMemoryMB     uint64
    Concurrency     uint64
    Profile         string
    HeadroomPct     int
    MemLimitMB      uint64
    MemLimitSource  string
    EffectiveCores  int
    CpuLimitSource  string
}

// CurrentConfig computes the current effective RaptorQ configuration without allocating a processor.
func CurrentConfig(ctx context.Context) RaptorQConfig {
    // 1) Detect resources
    memLimitMB, memSource := detectMemoryLimitMB()
    effCores, cpuSource := detectEffectiveCores()

    // 2) Apply headroom and select profile
    headroomPct := readInt("LUMERA_RQ_MEM_HEADROOM_PCT", 40, 0, 90)
    usableMemMB := computeUsableMem(memLimitMB, headroomPct)
    profile := selectProfile(memLimitMB, os.Getenv("CODEC_PROFILE"))

    // 3) Compute defaults and adjust by cores and per‑worker budget
    defMaxMemMB, defConcurrency := defaultLimitsForProfile(profile, usableMemMB)
    defConcurrency = adjustConcurrency(defConcurrency, effCores)
    defConcurrency = rebalancePerWorkerMem(defMaxMemMB, defConcurrency, 512)

    // 4) Apply env overrides
    symbolSize := uint16(readUint("LUMERA_RQ_SYMBOL_SIZE", uint64(raptorq.DefaultSymbolSize), 1024, 65535))
    redundancy := uint8(readUint("LUMERA_RQ_REDUNDANCY", 5, 1, 32))
    maxMemMB := readUint("LUMERA_RQ_MAX_MEMORY_MB", defMaxMemMB, 256, 1<<20)
    concurrency := readUint("LUMERA_RQ_CONCURRENCY", defConcurrency, 1, 1024)

    return RaptorQConfig{
        SymbolSize:     symbolSize,
        Redundancy:     redundancy,
        MaxMemoryMB:    maxMemMB,
        Concurrency:    concurrency,
        Profile:        profile,
        HeadroomPct:    headroomPct,
        MemLimitMB:     memLimitMB,
        MemLimitSource: memSource,
        EffectiveCores: effCores,
        CpuLimitSource: cpuSource,
    }
}

// detectMemoryLimitMB attempts to determine the memory limit (MB) from cgroups, falling back to MemTotal.
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
			if v > 1<<50 {
				// unlimited; fallthrough to MemTotal
			} else {
				return v / (1024 * 1024), "cgroupv1:memory.limit_in_bytes"
			}
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

// detectEffectiveCores attempts to determine CPU quota; returns cores and source.
func detectEffectiveCores() (int, string) {
	// cgroup v2: /sys/fs/cgroup/cpu.max: "max" or "<quota> <period>"
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		parts := strings.Fields(strings.TrimSpace(string(b)))
		if len(parts) == 2 && parts[0] != "max" {
			if quota, err1 := strconv.ParseUint(parts[0], 10, 64); err1 == nil {
				if period, err2 := strconv.ParseUint(parts[1], 10, 64); err2 == nil && period > 0 {
					cores := int(float64(quota) / float64(period))
					if cores < 1 {
						cores = 1
					}
					return cores, "cgroupv2:cpu.max"
				}
			}
		}
	}
	// cgroup v1: /sys/fs/cgroup/cpu/cpu.cfs_quota_us and cpu.cfs_period_us
	if qb, err1 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err1 == nil {
		if pb, err2 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us"); err2 == nil {
			qStr := strings.TrimSpace(string(qb))
			pStr := strings.TrimSpace(string(pb))
			if qStr != "-1" {
				if quota, errA := strconv.ParseUint(qStr, 10, 64); errA == nil {
					if period, errB := strconv.ParseUint(pStr, 10, 64); errB == nil && period > 0 {
						cores := int(float64(quota) / float64(period))
						if cores < 1 {
							cores = 1
						}
						return cores, "cgroupv1:cpu.cfs_quota_us/period"
					}
				}
			}
		}
	}
	return 0, "runtime.NumCPU"
}

func minNonZero(a, b uint64) uint64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// readUint reads an unsigned integer from env with bounds and a default.
func readUint(env string, def uint64, min uint64, max uint64) uint64 {
	if v, ok := os.LookupEnv(env); ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			if n < min {
				return min
			}
			if max > 0 && n > max {
				return max
			}
			return n
		}
	}
	return def
}

// readInt reads an integer from env with bounds and a default.
func readInt(env string, def int, min int, max int) int {
	if v, ok := os.LookupEnv(env); ok {
		if n, err := strconv.Atoi(v); err == nil {
			if n < min {
				return min
			}
			if max > 0 && n > max {
				return max
			}
			return n
		}
	}
	return def
}

// computeUsableMem applies a headroom percentage to a memory limit.
func computeUsableMem(memLimitMB uint64, headroomPct int) uint64 {
	if headroomPct <= 0 || memLimitMB == 0 {
		return memLimitMB
	}
	return uint64(math.Max(0, float64(memLimitMB)*(1.0-float64(headroomPct)/100.0)))
}

// selectProfile decides which profile to use based on a forced value or memory limit.
func selectProfile(memLimitMB uint64, forced string) string {
	p := strings.ToLower(strings.TrimSpace(forced))
	switch p {
	case "edge", "standard", "perf":
		return p
	}
	// Default to perf when not forced
	return "perf"
}

// defaultLimitsForProfile returns default max memory and concurrency for a profile.
func defaultLimitsForProfile(profile string, usableMemMB uint64) (uint64, uint64) {
	switch profile {
	case "edge":
		mm := minNonZero(usableMemMB, 1024)
		if mm == 0 {
			mm = 1024
		}
		return mm, 2
	case "standard":
		mm := uint64(math.Min(float64(usableMemMB)*0.6, 4*1024))
		if mm < 1024 {
			mm = 1024
		}
		return mm, 4
	default: // perf
		mm := uint64(math.Min(float64(usableMemMB)*0.6, 16*1024))
		return mm, 8
	}
}

// adjustConcurrency caps concurrency by effective cores.
func adjustConcurrency(defConcurrency uint64, effCores int) uint64 {
	cpu := runtime.NumCPU()
	if effCores > 0 {
		cpu = effCores
	}
	if cpu < 1 {
		cpu = 1
	}
	if defConcurrency > uint64(cpu) {
		return uint64(cpu)
	}
	return defConcurrency
}

// rebalancePerWorkerMem reduces concurrency until each worker has at least minPerWorkerMB.
func rebalancePerWorkerMem(defMaxMemMB uint64, defConcurrency uint64, minPerWorkerMB uint64) uint64 {
	if defConcurrency == 0 {
		return 1
	}
	for defMaxMemMB/defConcurrency < minPerWorkerMB && defConcurrency > 1 {
		defConcurrency--
	}
	return defConcurrency
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
