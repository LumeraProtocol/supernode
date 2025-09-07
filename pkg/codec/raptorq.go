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
	"sync"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

type raptorQ struct {
	symbolsBaseDir string
}

// activeProfile selects the highest-performing codec profile.
const activeProfile = "perf"

// codec defaults
const (
	defaultRedundancy = 5
	minPerWorkerMB    = 512
)

func NewRaptorQCodec(dir string) Codec {
	return &raptorQ{
		symbolsBaseDir: dir,
	}

}

// newProcessor constructs a RaptorQ processor using platform/resource detection
// and profile-based defaults (no environment configuration required).
func newProcessor(ctx context.Context) (*raptorq.RaptorQProcessor, error) {
	// 1) Detect resources (memory/CPU)
	memLimitMB, memSource := detectMemoryLimitMB()
	effCores, cpuSource := detectEffectiveCores()

	// 2) Select profile (hardcoded highest-performing profile)
	profile := activeProfile

	// 3) Apply profile-based headroom and compute usable memory
	headroomPct := defaultHeadroomForProfile(profile)
	usableMemMB := computeUsableMem(memLimitMB, headroomPct)

	// 4) Compute default limits for the chosen profile
	defMaxMemMB, defConcurrency := defaultLimitsForProfile(profile, usableMemMB)

	// 5) Adjust concurrency by effective cores/quotas
	defConcurrency = adjustConcurrency(defConcurrency, effCores)

	// 6) Ensure per-worker memory target (>=minPerWorkerMB) by reducing concurrency if needed
	defConcurrency = rebalancePerWorkerMem(defMaxMemMB, defConcurrency, minPerWorkerMB)

	// 7) Apply defaults derived from profile and detected resources
	symbolSize := uint16(raptorq.DefaultSymbolSize)
	redundancy := uint8(defaultRedundancy)
	maxMemMB := defMaxMemMB
	concurrency := defConcurrency

	// 8) Log final configuration
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

// RaptorQConfig describes the effective codec configuration derived from
// detected resources and profile defaults.
type RaptorQConfig struct {
	SymbolSize     uint16
	Redundancy     uint8
	MaxMemoryMB    uint64
	Concurrency    uint64
	Profile        string
	HeadroomPct    int
	MemLimitMB     uint64
	MemLimitSource string
	EffectiveCores int
	CpuLimitSource string
}

// CurrentConfig computes the current effective RaptorQ configuration without allocating a processor.
func CurrentConfig(ctx context.Context) RaptorQConfig {
	// 1) Detect resources
	memLimitMB, memSource := detectMemoryLimitMB()
	effCores, cpuSource := detectEffectiveCores()

	// 2) Select profile and apply profile-based headroom
	profile := activeProfile
	headroomPct := defaultHeadroomForProfile(profile)
	usableMemMB := computeUsableMem(memLimitMB, headroomPct)

	// 3) Compute defaults and adjust by cores and per‑worker budget
	defMaxMemMB, defConcurrency := defaultLimitsForProfile(profile, usableMemMB)
	defConcurrency = adjustConcurrency(defConcurrency, effCores)
	defConcurrency = rebalancePerWorkerMem(defMaxMemMB, defConcurrency, minPerWorkerMB)

	// 4) Apply defaults derived from profile and detected resources
	symbolSize := uint16(raptorq.DefaultSymbolSize)
	redundancy := uint8(defaultRedundancy)
	maxMemMB := defMaxMemMB
	concurrency := defConcurrency

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

// defaultHeadroomForProfile returns the default memory headroom percent for a profile.
// Lower headroom for perf to maximize throughput; higher for edge to be conservative.
func defaultHeadroomForProfile(profile string) int {
	switch profile {
	case "edge":
		return 40
	case "standard":
		return 30
	default: // perf
		return 20
	}
}

// detectMemoryLimitMB attempts to determine the memory limit (MB) from cgroups, falling back to MemTotal.
var (
	memOnce      sync.Once
	cachedMemMB  uint64
	cachedMemSrc string
)

func detectMemoryLimitMB() (uint64, string) {
	memOnce.Do(func() {
		// cgroup v2: /sys/fs/cgroup/memory.max
		if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
			s := strings.TrimSpace(string(b))
			if s != "max" {
				if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
					cachedMemMB = v / (1024 * 1024)
					cachedMemSrc = "cgroupv2:memory.max"
					return
				}
			}
		}
		// cgroup v1: /sys/fs/cgroup/memory/memory.limit_in_bytes
		if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
			s := strings.TrimSpace(string(b))
			if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
				// Some systems report a huge number when unlimited; treat > 1PB as unlimited
				if v <= 1<<50 {
					cachedMemMB = v / (1024 * 1024)
					cachedMemSrc = "cgroupv1:memory.limit_in_bytes"
					return
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
							cachedMemMB = kb / 1024
							cachedMemSrc = "meminfo:MemTotal"
							return
						}
					}
				}
			}
		}
		cachedMemMB = 0
		cachedMemSrc = "unknown"
	})
	return cachedMemMB, cachedMemSrc
}

// detectEffectiveCores attempts to determine CPU quota; returns cores and source.
var (
	coresOnce      sync.Once
	cachedCores    int
	cachedCoresSrc string
)

func detectEffectiveCores() (int, string) {
	coresOnce.Do(func() {
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
						cachedCores = cores
						cachedCoresSrc = "cgroupv2:cpu.max"
						return
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
							cachedCores = cores
							cachedCoresSrc = "cgroupv1:cpu.cfs_quota_us/period"
							return
						}
					}
				}
			}
		}
		cachedCores = 0
		cachedCoresSrc = "runtime.NumCPU"
	})
	return cachedCores, cachedCoresSrc
}

// (env-based readers removed; headroom and profile are managed internally)

// computeUsableMem applies a headroom percentage to a memory limit using integer math.
func computeUsableMem(memLimitMB uint64, headroomPct int) uint64 {
	if headroomPct <= 0 || memLimitMB == 0 {
		return memLimitMB
	}
	return memLimitMB - (memLimitMB*uint64(headroomPct))/100
}

// Profile is hardcoded via activeProfile for clarity and performance.

// defaultLimitsForProfile returns default max memory and concurrency for a profile.
func defaultLimitsForProfile(profile string, usableMemMB uint64) (uint64, uint64) {
	switch profile {
	case "edge":
		// conservative footprint for edge environments
		mm := uint64(math.Min(float64(usableMemMB)*0.5, 2*1024))
		if mm < 512 {
			mm = 512
		}
		return mm, 2
	case "standard":
		// balanced defaults: higher share of usable memory and cap
		mm := uint64(math.Min(float64(usableMemMB)*0.75, 8*1024))
		if mm < 1024 {
			mm = 1024
		}
		return mm, 6
	default: // perf
		// aggressive but safe: use ~80% of usable memory with a higher ceiling
		mm := uint64(math.Min(float64(usableMemMB)*0.8, 32*1024))
		if mm < 2048 {
			mm = 2048
		}
		// allow high default concurrency; capped by effective cores later
		return mm, 64
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

	// Create processor using detected resources and profile defaults
	processor, err := newProcessor(ctx)
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()
	logtrace.Info(ctx, "RaptorQ processor created", fields)

	/* ---------- 1.  run the encoder ---------- */
	blockSize := processor.GetRecommendedBlockSize(uint64(req.DataSize))
	logtrace.Info(ctx, "RaptorQ recommended block size", logtrace.Fields{"block_size": blockSize})

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
