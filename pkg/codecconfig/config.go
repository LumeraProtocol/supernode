package codecconfig

import (
    "context"
    "os"
    "strconv"
    "strings"
)

// Config describes the effective codec configuration (fixed policy; no env).
type Config struct {
    SymbolSize     uint16
    Redundancy     uint8
    MaxMemoryMB    uint64
    Concurrency    uint64
    HeadroomPct    int
    MemLimitMB     uint64
    MemLimitSource string
}

// Defaults mirrored from rq-go and current project conventions.
const (
    defaultSymbolSize = 65535
    defaultRedundancy = 5
    fixedConcurrency  = 4
    headroomPct       = 10
)

// Current computes the current effective codec configuration (fixed policy).
func Current(ctx context.Context) Config {
    memLimitMB, memSource := detectMemoryLimitMB()
    usableMemMB := computeUsableMem(memLimitMB, headroomPct)

    symbolSize := uint16(defaultSymbolSize)
    redundancy := uint8(defaultRedundancy)
    maxMemMB := usableMemMB
    concurrency := uint64(fixedConcurrency)

    return Config{
        SymbolSize:     symbolSize,
        Redundancy:     redundancy,
        MaxMemoryMB:    maxMemMB,
        Concurrency:    concurrency,
        HeadroomPct:    headroomPct,
        MemLimitMB:     memLimitMB,
        MemLimitSource: memSource,
    }
}

func computeUsableMem(memLimitMB uint64, headroomPct int) uint64 {
    if headroomPct <= 0 || memLimitMB == 0 {
        return memLimitMB
    }
    return memLimitMB - (memLimitMB*uint64(headroomPct))/100
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

// No CPU quota detection needed for fixed policy.
