# Cascade Artefacts Storage Flow

This document explains how Cascade artefacts (ID files + RaptorQ symbols) are persisted to the P2P network, the control flow from the API to the P2P layer, and which background workers continue the process after the API call returns.

## Scope & Terminology

- Artefacts: The data produced for a Cascade action that must be stored on the network.
  - ID files (a.k.a. redundant metadata files): compact metadata payloads derived from the layout/index.
  - Symbols: RaptorQ-encoded chunks of the input file.
- Request IDs and files are generated during the registration flow; storing starts after validation and simulation succeed.

## High‑Level Sequence

1) Client calls `Register` with input file and action metadata.
2) The service verifies the action, fee, eligibility, signature and layout consistency, then encodes the input into RaptorQ symbols.
3) Finalize simulation is performed on chain to ensure the action can finalize.
4) If simulation passes, artefacts are persisted:
   - ID files are stored first as a single batch.
   - Symbols are stored in batches; a first pass may downsample for large directories.
   - A background worker continues storing the remainder (no sampling) after the call returns.
5) Action is finalized on chain and control returns to the caller.

Code reference:
- `supernode/services/cascade/register.go` (Register flow, steps 1–11)
- `supernode/services/cascade/helper.go` (wrappers and helpers)
- `supernode/services/cascade/adaptors/p2p.go` (P2P adaptor for storage)
- `p2p/p2p.go`, `p2p/kademlia/dht.go`, `p2p/kademlia/rq_symbols.go` (P2P and Kademlia implementation)

## Register Flow Up To Storage

Register performs the following (simplified):

- Fetches and validates the on‑chain action.
- Verifies fee and that this node is in the top supernodes for the block height.
- Decodes cascade metadata and verifies that the uploaded data hash matches the ticket.
- Encodes the input using RaptorQ; produces `SymbolsDir` and `Metadata` (layout).
- Verifies layout signature (creator), generates RQ‑ID files and validates IDs.
- Simulates finalize (chain dry‑run). If simulation fails, the call returns with an error (no storage).
- Calls `storeArtefacts(...)` to persist artefacts to P2P.

Events are streamed throughout via `send(*RegisterResponse)`, including when artefacts are stored and when the action is finalized.

## The storeArtefacts Wrapper

Function: `supernode/services/cascade/helper.go::storeArtefacts`

- Thin pass‑through that packages a `StoreArtefactsRequest` and forwards to the P2P adaptor (`task.P2P.StoreArtefacts`).
- Parameters:
  - `IDFiles [][]byte`: the redundant metadata files to store.
  - `SymbolsDir string`: filesystem directory where symbols were written.
  - `TaskID string` and `ActionID string`: identifiers for logging and DB association.

Does not return metrics; logs provide visibility.

## P2P Adaptor: StoreArtefacts

Implementation: `supernode/services/cascade/adaptors/p2p.go`

1) Store metadata (ID files) using `p2p.Client.StoreBatch(...)`.

2) Store symbols using `storeCascadeSymbols(...)`:
   - Records the symbol directory in a small SQLite store: `rqStore.StoreSymbolDirectory(taskID, symbolsDir)`.
   - Walks `symbolsDir` to list symbol files. If there are more than 2,500 symbols, downsamples to 10% for this first pass (random sample, sorted deterministically afterward).
   - Streams symbols in fixed‑size batches of 2,500 files:
     - Each batch loads files, calls `p2p.Client.StoreBatch(...)` with a 5‑minute timeout, and deletes successfully uploaded files.
   - Marks “first batch stored” for this action: `rqStore.UpdateIsFirstBatchStored(actionID)`.
   - Logs counts and timings; no metrics are returned.

3) Return:
   - No metrics aggregation; return indicates success/failure only.

Notes:
- This adaptor only performs a first pass of symbol storage. For large directories it may downsample; the background worker completes the remaining symbols later (see Background Worker section).

## P2P Client and DHT: StoreBatch

`p2p.Client.StoreBatch` proxies to `DHT.StoreBatch`:

- Local persist first: `store.StoreBatch(ctx, values, typ, true)` ensures local DB/storage contains the items.
- Network store: `DHT.IterateBatchStore(ctx, values, typ, taskID)`:
  - For each value, compute its Blake3 hash; compute the top‑K closest nodes from the routing table.
  - Build a node→items map and invoke `batchStoreNetwork(...)` with bounded concurrency (a goroutine per node, limited via a semaphore; all joined before returning).
  - If the measured success rate is below an internal threshold, DHT returns an error.

Important distinctions:
- `requests` is the number of per‑node RPCs attempted; it is not the number of items in the batch.
- Success rate is based on successful node acknowledgements divided by `requests`.

## Metrics & Events

`Register` logs and emits an informational event (Artefacts stored), then proceeds to finalize the action on chain.

## Background Worker (Symbols Continuation)

Started in DHT `run()` when P2P service starts:

- Function: `p2p/kademlia/rq_symbols.go::startStoreSymbolsWorker`
- Every 30 seconds:
  - Queries `rq_symbols_dir` for rows where `is_first_batch_stored = TRUE` and `is_completed = FALSE`.
  - For each directory, scans and stores ALL remaining symbols (no sampling) in 1,000‑file batches using the same `StoreBatch` API.
  - Deletes files after successful upload.
  - Marks the directory as completed: `rqstore.SetIsCompleted(txid)`.

Effectively, the API call performs a first pass, and the background worker ensures eventual completion.

## Storage Bookkeeping (SQLite)

Table: `rq_symbols_dir`

- Columns:
  - `txid TEXT PRIMARY KEY` — action/task identifier.
  - `dir TEXT NOT NULL` — filesystem path to the symbols directory.
  - `is_first_batch_stored BOOLEAN NOT NULL DEFAULT FALSE` — set true after first pass completes.
  - `is_completed BOOLEAN NOT NULL DEFAULT FALSE` — set true after the background worker completes.
  - `created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`.

APIs:
- `StoreSymbolDirectory(txid, dir)` — insert entry when first pass starts.
- `UpdateIsFirstBatchStored(txid)` — mark first pass completion.
- `GetToDoStoreSymbolDirs()` — list txids/dirs awaiting background completion.
- `SetIsCompleted(txid)` — mark directory as fully processed.

## Timeouts, Limits, and Knobs

- First‑pass symbol batches: 2,500 items; per‑batch timeout: 5 minutes.
- Sampling threshold: if symbol count > 2,500, downsample to 10% for first pass.
- DHT minimum success rate: 75% — batch returns error if not met.
- Background worker batch size: 1,000; runs every 30 seconds; no sampling.

These values can be tuned in:
- `supernode/services/cascade/adaptors/p2p.go` (batching, sampling for first pass).
- `p2p/kademlia/rq_symbols.go` (background worker interval and batch size).
- `p2p/kademlia/dht.go` (minimum success rate, internal concurrencies).

## Error Handling & Return Semantics

- If finalize simulation fails: Register returns an error before any storage.
- If metadata store fails: `StoreArtefacts` returns error; Register wraps and returns.
- If symbol first pass fails: same; background worker does not start because `is_first_batch_stored` is not set.
- If the network success rate is below the threshold: DHT returns an error; adaptor propagates it.
- File I/O errors (load/delete) abort the corresponding batch with a wrapped error.

## Concurrency Model

- Within `StoreArtefacts` → `DHT.StoreBatch`, network calls are concurrent (goroutines per node) but **joined before return**. There is no detached goroutine in the first pass.
- The only long‑running background activity is the P2P‑level worker (`startStoreSymbolsWorker`) launched when the P2P service starts, not by the API call itself.

## Cleanup Behavior

- First pass deletes uploaded symbol files per batch (`utils.DeleteSymbols`) after a successful store batch.
- Background worker also deletes files after each batch store.
- The uploaded raw input file is removed by `Register` in a `defer` block regardless of outcome.
