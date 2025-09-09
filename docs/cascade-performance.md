# Cascade Downloads & Performance: Concepts, Limits, and Tuning

This document explains how Cascade encoding/decoding works, the performance and memory factors involved, and practical configuration guidance. It consolidates the “blocks and symbols” primer and expands it with deeper operational tuning, error references, and code pointers — in a concise, professional format.

## Overview

- Cascade uses RaptorQ forward error correction to split a file into blocks and symbols that can be stored/fetched from a P2P network.
- Decoding requires enough symbols to reconstruct each block; integrity is verified with hashes recorded in the layout.
- Performance and reliability are driven by four main levers: block size, redundancy, concurrency, and memory headroom. Batching and ordering in the store path, and supernode selection in the download path, also matter.

## Current Defaults (Implementation)

- RaptorQ (codec)
  - Block cap: 256 MB (encode‑time upper bound per block)
  - Decode concurrency: 1
  - Memory headroom: 20% of detected RAM
  - Symbol size: ~65,535 bytes
  - Redundancy: 5

- Store path (foreground adaptor)
  - Batch size: 2,500 files per batch (≈156 MiB typical at default symbol size)
  - Downsampling: if total files > 2,500, take 10% sorted prefix for initial store
  - Per‑batch P2P store timeout: 5 minutes

- Store path (background worker)
  - Batch size: 1,000 files per batch (≈62.5 MiB typical)

- Download path
  - SDK per‑supernode download deadline: 10 minutes
  - Supernode ranking: status probe ~2 seconds per node; sorted by available memory (desc)
  - P2P exec timeouts (per RPC):
    - FindValue: 5s
    - BatchFindValues: 60s
    - BatchGetValues: 75s
    - StoreData: 10s
    - BatchStoreData: 75s
    - Replicate: 90s

- Upload constraints
  - Max file size: 1 GB (enforced in SDK and server)
  - Adaptive upload chunk size: ~64 KB → 4 MB based on file size

## Core Concepts

- Block: A contiguous segment of the original file. Think of it as a “chapter”.
- Symbol: A small piece produced by RaptorQ for a block. You only need “enough” symbols to reconstruct the block.
- Layout: Metadata that lists all blocks (block_id, size, original offset, per‑block hash) and the symbol IDs belonging to each block.

Encode (upload):
- Choose a block size; RaptorQ creates symbols per block; symbols + layout are stored.

Decode (download):
- Fetch symbols from the network; reconstruct each block independently; write each block back at its original offset; verify hashes; stream the file.

Key facts:
- Symbols never mix across blocks.
- Peak memory during decode scales roughly with the chosen block size (plus overhead).

## File Size Limits & Upload Chunking

- Maximum file size: 1 GB (enforced both in SDK and server handlers).
- Adaptive upload chunk size: ~64 KB → 4 MB depending on total file size for throughput vs memory stability.

## Encoding/Decoding Workflow (high level)

1) SDK uploads file to a supernode (gRPC stream). Server writes to a temporary file, validates size and integrity.
2) Server encodes with RaptorQ: produces a symbols directory and a layout JSON.
3) Server stores artefacts: layout/ID files and symbols into P2P in batches.
4) Later, SDK requests download; supernode fetches symbols progressively and decodes to reconstruct the file; integrity is verified.

## Contexts & Timeouts (download path)

- SDK: wraps the download RPC with a 10‑minute deadline.
- Server: uses that context; P2P layer applies per‑RPC timeouts (e.g., 5s for single key FindValue, ~75s for BatchGetValues), with internal early cancellation once enough symbols are found.
- RaptorQ: uses the same context for logging; no additional deadline inside decode.

## Memory Model

- Decoder memory is primarily a function of block size and concurrency.
- Headroom percentage reduces the usable memory budget to leave safety buffer for the OS and other processes.
- Example formula: usable_memory ≈ TotalRAM × (1 − headroom%).

## Configuration Levers

The implementation uses simple fixed constants for safety and predictability. You can adjust them and rebuild.

1) Block Size Cap (`targetBlockMB`, encode‑time)
- What: Upper bound on block size. Actual used size = min(recommended_by_codec, cap).
- Effect: Smaller cap lowers peak decode memory (more blocks, more symbols/keys). Larger cap reduces block count (faster on big machines) but raises peak memory.
- Current default: 256 MB (good balance on well-provisioned machines). Only affects newly encoded artefacts.

2) Redundancy (`defaultRedundancy`, encode‑time)
- What: Extra protection (more symbols) to tolerate missing data.
- Effect: Higher redundancy improves recoverability but costs more storage and network I/O. Does not materially change peak memory.
- Current default: 5 (good real‑world trade‑off).

3) Concurrency (`fixedConcurrency`, decode‑time)
- What: Number of RaptorQ decode workers.
- Effect: Higher is faster but multiplies memory; lower is safer and predictable.
- Current default: 1 (safe default for wide environments).

4) Headroom (`headroomPct`, decode‑time)
- What: Percentage of detected RAM left unused by the RaptorQ processor.
- Effect: More headroom = safer under load; less headroom = more memory available to decode.
- Current default: 20% (conservative and robust for shared hosts).

## Batching Strategy (store path)

Why batching matters:
- Store batches are loaded wholly into memory before sending to P2P.
- A fixed “files‑per‑batch” limit gives variable memory usage because symbol files can differ slightly in size.

Current defaults:
- Foreground adaptor: `loadSymbolsBatchSize = 2500` → ≈ 2,500 × 65,535 B ≈ 156 MiB per batch (typical).
- Background worker: `loadSymbolsBatchSize = 1000` → ≈ 62.5 MiB per batch.

Byte‑budget alternative (conceptual, not implemented):
- Cap the total bytes per batch (e.g., 128–256 MiB), with a secondary cap on file count.
- Benefits: predictable peak memory; better throughput on small symbols; avoids spikes on larger ones.

## Ordering for Throughput (store path)

- We sort relative file paths before batching (e.g., `block_0/...`, `block_1/...`) to improve filesystem locality and reduce disk seeks. This favors speed.
- Trade‑off: If a process stops mid‑way, earlier blocks (lexicographically smaller) are more likely stored than later ones. For fairness across blocks at partial completion, interleaving could be used at some CPU cost.

## Supernode Selection (download path)

- The SDK ranks supernodes by available memory (fast 2s status probe per node) and attempts downloads in that order.
- This increases the chances of successful decode for large files.

## Defaults & Suggested Settings

1 GB files (general)
- Block cap: 256 MB (≈4 blocks)
- Concurrency: 1
- Headroom: 20%
- Redundancy: 5

Large‑memory machines (performance‑leaning)
- Block cap: 256 MB (or 512 MB) to reduce block count and increase throughput.
- Concurrency: 1–2.
- Headroom: 15–20% depending on other workloads.
- Redundancy: 5 (or 6 in sparse networks).

Small‑memory machines
- Block cap: 64–128 MB
- Concurrency: 1
- Headroom: 20%
- Redundancy: 5

## Error Reference

- memory limit exceeded
  - The decoder exceeded its memory budget. Reduce block size or concurrency, increase RAM, or lower headroom.

- hash mismatch for block X
  - Data reconstructed for the block did not match the expected hash. Often indicates wrong/corrupt symbols; can also occur when decoding fails mid‑way under memory pressure. Re‑fetching or re‑encoding may be required.

- insufficient symbols
  - Not enough valid symbols were available; the retriever will fetch more.

- gRPC Internal on download stream
  - The supernode returned an error during decode (e.g., memory failure). The SDK will try the next supernode.

## Code Pointers

- Block cap, headroom, concurrency (RaptorQ): `pkg/codec/raptorq.go`
- Store batching (foreground path): `supernode/services/cascade/adaptors/p2p.go`
- Store batching (background worker): `p2p/kademlia/rq_symbols.go`
- Batch symbol loading / deletion: `pkg/utils/utils.go` (LoadSymbols, DeleteSymbols)
- Supernode ranking by memory (download): `sdk/task/download.go`
- File size cap & adaptive upload chunking: SDK and server sides (`sdk/adapters/supernodeservice/adapter.go`, `supernode/node/action/server/cascade/cascade_action_server.go`)

## Notes & Scope

- Changing block size only affects new encodes; existing artefacts keep their original layout.
- Tuning should reflect your fleet: prefer safety defaults for heterogeneous environments; be aggressive only on known large‑RAM hosts.

## FAQ

- Why might a smaller file decode but a larger file fail?
  - Peak memory grows with data size and chosen block size. A smaller file may fit within the decoder’s memory budget on a given machine, while a larger one may exceed it. Smaller blocks and/or more RAM resolve this.

- Does changing block size affect old files?
  - No. It only affects newly encoded content. Existing artefacts retain their original layout.

- Will smaller blocks slow things down?
  - Slightly, due to more pieces and network lookups. For constrained machines, the reliability gain outweighs the small performance cost.

- What’s the best block size?
  - There’s no single best value. 128 MB is a solid default. Use 64 MB for smaller machines and 256–512 MB for large servers when maximizing throughput.
