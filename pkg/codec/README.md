# Codec (RaptorQ) Guide

## Table of Contents
- [Overview](#overview)
- [What It Does](#what-it-does)
- [Behavioural Note](#behavioural-note)
- [What We Don’t Do](#what-we-dont-do)
- [Quick Defaults (no env)](#quick-defaults-no-env)
- [Change Behavior (simple rules)](#change-behavior-simple-rules)
- [Profiles (behavior overview)](#profiles-behavior-overview)
- [Where It Applies](#where-it-applies)
- [Integration](#integration)
- [Notes](#notes)
- [Log Observability](#log-observability)
- [P2P interaction (high‑level)](#p2p-interaction-highlevel)
- [Examples (no env)](#examples-no-env)
- [Minimal Usage (Decode)](#minimal-usage-decode)
- [Minimal Usage (Encode)](#minimal-usage-encode)
- [Troubleshooting](#troubleshooting)

## Overview
- Thin, well‑scoped API around RaptorQ for encoding/decoding cascade artefacts.
- Safe defaults and memory‑conscious behavior tuned for files up to 1 GB.

## What It Does
- Per‑request processor: create/free a processor per Encode/Decode to bound native memory.
- Decode hygiene: write symbols to disk and drop in‑memory buffers before decoding to lower peaks.
- Layout file: stored as `_raptorq_layout.json` in the request’s symbols directory.
- Directory layout:
  - Encode → `<symbolsBaseDir>/<taskID>/`
  - Decode → `<symbolsBaseDir>/<actionID>/` (decoded output sits alongside)

## Behavioural Note
- Decode mutates input: it deletes entries from `DecodeRequest.Symbols` after flushing to disk to free memory.

## What We Don’t Do
- Require env setup: runs fine without any env vars. If set, env vars override the defaults.
- Full pre‑fetch of symbols: progressive retrieval avoids over‑fetching and reduces memory pressure.
- Long‑lived processors: we create/free a processor per request to bound native memory.

## Quick Defaults (no env)
- Profile: perf (fastest defaults)
- Headroom: `LUMERA_RQ_MEM_HEADROOM_PCT=40` → usable_mem = limit × (1−0.40)
- Max memory: `min(0.6 × usable_mem, 16 GiB)`
- Concurrency: `min(8, effective_cores)`, then reduced so `max_memory_mb / concurrency ≥ 512 MB`
- Symbol size: `65535`  •  Redundancy: `5`

## Change Behavior (simple rules)
- Pick profile: set `CODEC_PROFILE=edge|standard|perf` (perf is default)
- Reserve headroom: set `LUMERA_RQ_MEM_HEADROOM_PCT` (0–90, default 40)
- Override knobs directly (take precedence over profile):
  - `LUMERA_RQ_MAX_MEMORY_MB`, `LUMERA_RQ_CONCURRENCY`, `LUMERA_RQ_SYMBOL_SIZE`, `LUMERA_RQ_REDUNDANCY`
- Detection sources: memory from cgroups v2/v1 (fallback `/proc/meminfo`), CPU from cgroup quota or `runtime.NumCPU()`

## Profiles (behavior overview)

| Profile   | Default selection | Max memory (default)                           | Concurrency (default)                | CPU cap                       | Min per‑worker MB | Symbol size | Redundancy | Env overrides                         |
|-----------|-------------------|-----------------------------------------------|--------------------------------------|-------------------------------|-------------------|-------------|------------|----------------------------------------|
| `edge`    | Only when forced  | `min(usable_mem, 1 GiB)`                      | `2`                                  | Capped by effective cores     | `≥ 512`           | 65535       | 5          | `LUMERA_RQ_*`, `CODEC_PROFILE=edge`    |
| `standard`| Only when forced  | `min(0.6 × usable_mem, 4 GiB)`                | `4`                                  | Capped by effective cores     | `≥ 512`           | 65535       | 5          | `LUMERA_RQ_*`, `CODEC_PROFILE=standard`|
| `perf`    | Default           | `min(0.6 × usable_mem, 16 GiB)`               | `8`                                  | Capped by effective cores     | `≥ 512`           | 65535       | 5          | `LUMERA_RQ_*`, `CODEC_PROFILE=perf`   |

- usable_mem = memory_limit × (1 − `LUMERA_RQ_MEM_HEADROOM_PCT`/100). Default headroom = 40%.
- Effective cores: derived from cgroup CPU quota (v2 `cpu.max`, v1 `cpu.cfs_*`) or `runtime.NumCPU()`.
- Per‑worker memory: if `max_memory_mb / concurrency < 512`, concurrency is reduced until the target is met (down to 1).
- Env overrides: any of `LUMERA_RQ_SYMBOL_SIZE`, `LUMERA_RQ_REDUNDANCY`, `LUMERA_RQ_MAX_MEMORY_MB`, `LUMERA_RQ_CONCURRENCY` supersede the profile defaults.

## Where It Applies
- Encode: `pkg/codec/raptorq.go::Encode()` → compute block size, `EncodeFile()`, read layout
- Decode: `pkg/codec/decode.go::Decode()` → write symbols + layout to disk, `DecodeSymbols()`
- Progressive decode: `supernode/supernode/services/cascade/progressive_decode.go` escalates required symbols (9%, 25%, 50%, 75%, 100%)

## Integration
- Adaptors: `supernode/supernode/services/cascade/adaptors/rq.go` bridge the codec to higher‑level services
- Download flow: `supernode/supernode/services/cascade/download.go` uses progressive decode and verifies final file hash

## Notes
- Error code vs message: treat the error code as authoritative; message is context only
- Disk usage: ensure `<symbolsBaseDir>` has space for symbols and the final file
- Cleanup: callers delete the decode temp dir (`DecodeTmpDir`)
- File size: defaults suit ≤1 GiB; use overrides or profiles for larger files

## Log Observability
- On processor creation we log: symbol_size, redundancy_factor, max_memory_mb, concurrency, profile, headroom_pct, mem_limit and source, effective_cores and source.

## P2P interaction (high‑level)
- Codec redundancy and DHT replication are independent; progressive decode needs “enough” distinct symbols, not all of them

## Examples (no env)
- 16 GiB limit, 8 cores → usable=9.6 GiB → max=5.76 GiB → conc=8 → ~720 MB/worker
- 8 GiB limit, 4 cores → usable=4.8 GiB → max=2.88 GiB → conc=4 → ~720 MB/worker
- 2 GiB limit, 2 cores → usable=1.2 GiB → max=0.72 GiB → conc=1 (to keep ≥512 MB/worker)

## Minimal Usage (Decode)
```go
resp, err := codecImpl.Decode(ctx, codec.DecodeRequest{
    ActionID: actionID,
    Layout:   layout,
    Symbols:  symbols, // map[id]payload
})
// resp.Path is the reconstructed file; resp.DecodeTmpDir holds symbols + layout
```

## Minimal Usage (Encode)
```go
resp, err := codecImpl.Encode(ctx, codec.EncodeRequest{
    TaskID:   taskID,
    Path:     inputPath,
    DataSize: sizeBytes,
})
// resp.SymbolsDir contains symbols; resp.Metadata holds the layout to publish
```

## Troubleshooting
- “memory limit exceeded” with integrity text: treat the error code as authoritative; the message may include integrity hints.
- Decode failures: progressive helper escalates symbol count automatically; non‑integrity errors bubble up immediately.
