package codecconfig

import (
    "context"
    "math"
    "os"
    "runtime"
    "strconv"
    "strings"
)

// Config describes the effective codec configuration derived from env, resources and defaults.
type Config struct {
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

// Defaults mirrored from rq-go and current project conventions.
const (
    defaultSymbolSize  = 65535
    defaultRedundancy  = 5
    minPerWorkerMB     = 512
)

// Current computes the current effective codec configuration without requiring cgo.
func Current(ctx context.Context) Config {
    memLimitMB, memSource := detectMemoryLimitMB()
    effCores, cpuSource := detectEffectiveCores()

    headroomPct := readInt("LUMERA_RQ_MEM_HEADROOM_PCT", 40, 0, 90)
    usableMemMB := computeUsableMem(memLimitMB, headroomPct)

    profile := selectProfile(os.Getenv("CODEC_PROFILE"))
    // Support alternative name for profile if provided
    if p := strings.TrimSpace(os.Getenv("LUMERA_RQ_PROFILE")); p != "" {
        profile = selectProfile(p)
    }

    defMaxMemMB, defConcurrency := defaultLimitsForProfile(profile, usableMemMB)
    defConcurrency = adjustConcurrency(defConcurrency, effCores)
    defConcurrency = rebalancePerWorkerMem(defMaxMemMB, defConcurrency, minPerWorkerMB)

    symbolSize := uint16(readUint("LUMERA_RQ_SYMBOL_SIZE", uint64(defaultSymbolSize), 1024, 65535))
    redundancy := uint8(readUint("LUMERA_RQ_REDUNDANCY", uint64(defaultRedundancy), 1, 32))
    maxMemMB := readUint("LUMERA_RQ_MAX_MEMORY_MB", defMaxMemMB, 256, 1<<20)
    concurrency := readUint("LUMERA_RQ_CONCURRENCY", defConcurrency, 1, 1024)

    return Config{
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

func computeUsableMem(memLimitMB uint64, headroomPct int) uint64 {
    if headroomPct <= 0 || memLimitMB == 0 {
        return memLimitMB
    }
    return uint64(math.Max(0, float64(memLimitMB)*(1.0-float64(headroomPct)/100.0)))
}

func selectProfile(forced string) string {
    p := strings.ToLower(strings.TrimSpace(forced))
    switch p {
    case "edge", "standard", "perf":
        return p
    }
    // Default to perf when not forced
    return "perf"
}

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

func rebalancePerWorkerMem(defMaxMemMB uint64, defConcurrency uint64, minPerWorkerMB uint64) uint64 {
    if defConcurrency == 0 {
        return 1
    }
    for defMaxMemMB/defConcurrency < minPerWorkerMB && defConcurrency > 1 {
        defConcurrency--
    }
    return defConcurrency
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
