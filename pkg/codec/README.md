Codec (RaptorQ) Usage Guide

Purpose
- Provides a thin, well‑scoped API around the RaptorQ erasure coding engine for encoding and decoding cascade artefacts.
- Centralizes safe defaults and memory‑conscious behavior suitable for files up to 1 GB.

What We Do
- Per‑request processor lifecycle: create a processor per Encode/Decode call and Free it immediately after to avoid long‑lived native allocations.
- Decode hygiene: write incoming symbols to disk and drop their in‑memory buffers before calling the native decoder to minimize heap + native overlap.
- Layout file naming: store layout as `_raptorq_layout.json` inside the symbols directory used for the request. The decoder takes the explicit path; the name is for operator clarity.
- Directory layout:
  - Encode: symbols are written under `<symbolsBaseDir>/<taskID>/`.
  - Decode: symbols are written under `<symbolsBaseDir>/<actionID>/` and the decoded output file is placed alongside.

Behavioural Note (Decode mutates input)
- Decode deletes entries from `DecodeRequest.Symbols` as they are flushed to disk. This is intentional to free memory early. Do not reuse the map after calling Decode.

What We Don’t Do
- No environment configuration required: deployments do not set or rely on env vars. The defaults below are used automatically.
- No up‑front full fetch of symbols in the download path: the download service uses progressive retrieval (outside this package) to avoid over‑fetching and reduce memory pressure.
- No persistent shared processor: we avoid sharing a single processor across requests to keep native memory bounded and lifecycle explicit.

Defaults (chosen for ≤ 1 GB files)
- Symbol size: 65535 bytes
- Redundancy: 5
- Max memory cap: 8 GB
- Concurrency: min(NumCPU, 8)

Where These Apply
- Encode: `supernode/pkg/codec/raptorq.go::Encode()` creates a processor and uses `GetRecommendedBlockSize()` from the library, then `EncodeFile()` and reads the generated layout.
- Decode: `supernode/pkg/codec/decode.go::Decode()` writes symbols + layout to disk, then calls `DecodeSymbols()`.
- Progression logic: implemented in `supernode/supernode/services/cascade/progressive_decode.go`, not in this package. This helper escalates required symbol count (9%, 25%, 50%, 75%, 100%).

Integration Points
- Adaptors: `supernode/supernode/services/cascade/adaptors/rq.go` bridges the codec interface to higher‑level services.
- Download flow: `supernode/supernode/services/cascade/download.go` calls the progressive helper, then verifies the final file hash against on‑chain metadata.

Caveats & Notes
- Error categories vs messages: the native library may return an error code (e.g., memory limit) while the detail string mentions integrity (e.g., block hash mismatch). The Go binding maps codes per header; consumers should treat the code as authoritative and the message as context.
- Disk usage: decode writes all received symbols to disk. Ensure `<symbolsBaseDir>` has sufficient free space for worst‑case symbol sets and the final file.
- Cleanup: callers are responsible for deleting the decode temp directory (exposed as `DecodeTmpDir`).
- File size target: defaults are tuned for up to 1 GB artefacts. Larger files may require different caps and concurrency; our deployment does not rely on env var tuning by default.

Alignment with P2P Redundancy
- RaptorQ redundancy (repair symbols) and DHT replication are separate layers:
  - Codec produces all symbols (source + repair) according to the encoder’s redundancy settings.
  - P2P stores each symbol as an independent key and replicates values across the network per Kademlia (e.g., replicated to closest `Alpha` nodes).
- No direct configuration coupling is required: the network treats symbols as opaque blobs; decode only needs “enough” distinct symbols, which the progressive retrieval strategy handles.
- Operational implication: higher RaptorQ redundancy improves tolerance to missing symbols from peers; DHT replication improves availability of each symbol. Together they determine effective resilience.

Minimal Example (Decode)
```go
// Build symbol map and layout upstream; then:
resp, err := codecImpl.Decode(ctx, codec.DecodeRequest{
    ActionID: actionID,
    Layout:   layout,
    Symbols:  symbols, // map[id]payload
})
// resp.Path is the reconstructed file, resp.DecodeTmpDir holds symbols + layout
```

Minimal Example (Encode)
```go
resp, err := codecImpl.Encode(ctx, codec.EncodeRequest{
    TaskID:   taskID,
    Path:     inputPath,
    DataSize: sizeBytes,
})
// resp.SymbolsDir contains symbols; resp.Metadata holds the layout for publishing
```

Troubleshooting
- “memory limit exceeded” with integrity message: likely a native library mismatch of code/message. Treat the code as the handler (e.g., retry with fewer concurrent tasks or escalate symbols via progressive fetch).
- Decode failures: the progressive helper will escalate symbol count automatically. Non‑integrity errors bubble up immediately.
