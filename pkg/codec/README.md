# Codec (RaptorQ) Guide

## Overview
- Thin, well‑scoped API around RaptorQ for encoding/decoding cascade artefacts.
- Fixed policy (no env vars, no profiles), tuned for predictable behavior.

## What It Does
- Per‑request processor: create/free a processor per Encode/Decode to bound native memory.
- Decode hygiene: write symbols to disk and drop in‑memory buffers before decoding to lower peaks.
- Layout file: stored as `_raptorq_layout.json` in the request’s symbols directory.
- Directory layout:
  - Encode → `<symbolsBaseDir>/<taskID>/`
  - Decode → `<symbolsBaseDir>/<actionID>/` (decoded output sits alongside)

## Fixed Policy
- Concurrency: `4`
- Symbol size: `65535`
- Redundancy: `5`
- Max memory: system/cgroup memory minus `10%` headroom (no environment overrides)

## Where It Applies
- Encode: `pkg/codec/raptorq.go::Encode()` → compute block size, `EncodeFile()`, read layout
- Decode: `pkg/codec/decode.go::Decode()` → write symbols + layout to disk, `DecodeSymbols()`

## Notes
- Error code vs message: treat the error code as authoritative; message is context only
- Disk usage: ensure `<symbolsBaseDir>` has space for symbols and the final file
- Cleanup: callers delete the decode temp dir (`DecodeTmpDir`)

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
