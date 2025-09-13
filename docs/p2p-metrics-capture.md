# P2P Metrics Capture — What Each Field Means and Where It’s Collected

This guide explains every field we emit in Cascade events, how it is measured, and exactly where it is captured in the code.

The design is minimal by intent:
- Metrics are collected only for the first pass of Register (store) and for the active Download operation.
- P2P APIs return errors only; per‑RPC details are captured via a small metrics package (`pkg/p2pmetrics`).
- No aggregation; we only group raw RPC attempts by IP.

---

## Store (Register) Event

Event payload shape

```json
{
  "store": {
    "duration_ms": 9876,
    "symbols_first_pass": 220,
    "symbols_total": 1200,
    "id_files_count": 14,
    "calls_by_ip": {
      "10.0.0.5": [
        {"ip": "10.0.0.5", "address": "A:4445", "keys": 100, "success": true,  "duration_ms": 120},
        {"ip": "10.0.0.5", "address": "A:4445", "keys": 120, "success": false, "error": "timeout", "duration_ms": 300}
      ]
    }
  }
}
```

### Fields

- `store.duration_ms`

- `store.symbols_first_pass`
  - Meaning: Number of symbols sent during the Register first pass (across the combined first batch and any immediate first‑pass symbol batches).
  - Where captured: `supernode/services/cascade/adaptors/p2p.go` via `p2pmetrics.SetStoreSummary(...)` using the value returned by `storeCascadeSymbolsAndData`.

- `store.symbols_total`
  - Meaning: Total symbols available in the symbol directory (before sampling). Used to contextualize the first‑pass coverage.
  - Where captured: Computed in `storeCascadeSymbolsAndData` and included in `SetStoreSummary`.

- `store.id_files_count`
  - Meaning: Number of redundant metadata files (ID files) sent in the first combined batch.
  - Where captured: `len(req.IDFiles)` in `StoreArtefacts`, passed to `SetStoreSummary`.
  - Meaning: End‑to‑end elapsed time of the first‑pass store phase (Register’s storage section only).
  - Where captured: `supernode/services/cascade/adaptors/p2p.go`
    - A `time.Now()` timestamp is taken just before the first‑pass store function and measured on return.

- `store.calls_by_ip`
  - Meaning: All raw network store RPC attempts grouped by the node IP.
  - Each array entry is a single RPC attempt with:
    - `ip` — Node IP (fallback to `address` if missing).
    - `address` — Node string `IP:port`.
    - `keys` — Number of items in that RPC attempt (metadata + first symbols for the first combined batch, symbols for subsequent batches within the first pass).
    - `success` — True if the node acknowledged the store successfully.
    - `error` — Any error string captured; omitted when success.
    - `duration_ms` — RPC duration in milliseconds.
  - Where captured:
    - Emission point (P2P): `p2p/kademlia/dht.go::IterateBatchStore(...)`
      - After each node RPC returns, we call `p2pmetrics.RecordStore(taskID, Call{...})`.
      - `taskID` is read from the context via `p2pmetrics.TaskIDFromContext(ctx)`.
    - Grouping: `pkg/p2pmetrics/metrics.go`
      - `StartStoreCapture(taskID)` enables capture; `StopStoreCapture(taskID)` disables it.
      - Calls are grouped by `ip` (fallback to `address`) without further aggregation.

### First‑Pass Success Threshold

- Internal enforcement only: if DHT first‑pass success rate is below 75%, `IterateBatchStore` returns an error.
- No success rate is emitted in events; only error flow is affected.
- Code: `p2p/kademlia/dht.go::IterateBatchStore`.

### Scope Limits

- Background worker (which continues storing remaining symbols) is NOT captured — we don’t set a metrics task ID on those paths.

---

## Download (Retrieve) Event

Event payload shape

```json
{
  "retrieve": {
    "found_local": 42,
    "retrieve_ms": 2000,
    "decode_ms": 8000,
    "calls_by_ip": {
      "10.0.0.7": [
        {"ip": "10.0.0.7", "address": "B:4445", "keys": 13, "success": true, "duration_ms": 90}
      ]
    }
  }
}
```

### Fields

- `retrieve.found_local`
  - Meaning: Number of items retrieved from local storage before any network calls.
  - Where captured: `p2p/kademlia/dht.go::BatchRetrieve(...)`
    - After `fetchAndAddLocalKeys`, we call `p2pmetrics.ReportFoundLocal(taskID, int(foundLocalCount))`.
    - `taskID` is read from context with `p2pmetrics.TaskIDFromContext(ctx)`.

- `retrieve.retrieve_ms`
  - Meaning: Time spent in network batch‑retrieve.
  - Where captured: `supernode/services/cascade/download.go`
    - Timestamp before `BatchRetrieve`, measured after it returns.

- `retrieve.decode_ms`
  - Meaning: Time spent decoding symbols and reconstructing the file.
  - Where captured: `supernode/services/cascade/download.go`
    - Timestamp before decode, measured after it returns.

- `retrieve.calls_by_ip`
  - Meaning: All raw per‑RPC retrieve attempts grouped by node IP.
  - Each array entry is a single RPC attempt with:
    - `ip`, `address` — Identifiers as available.
    - `keys` — Number of symbols returned by that node in that call.
    - `success` — True if `keys > 0`.
    - `error` — Error string when the RPC failed; omitted otherwise.
    - `duration_ms` — RPC duration in milliseconds.
  - Where captured:
    - Emission point (P2P): `p2p/kademlia/dht.go::iterateBatchGetValues(...)`
      - Each node RPC records a `p2pmetrics.RecordRetrieve(taskID, Call{...})`.
      - `taskID` is extracted from context using `p2pmetrics.TaskIDFromContext(ctx)`.
    - Grouping: `pkg/p2pmetrics/metrics.go` (same grouping/fallback as store).

### Scope Limits

- Metrics are captured only for the active Download call (context is tagged in `download.go`).

---

## Context Tagging (Task ID)

- We use an explicit, metrics‑only context key defined in `pkg/p2pmetrics` to tag P2P calls with a task ID.
  - Setters: `p2pmetrics.WithTaskID(ctx, id)`.
  - Getters: `p2pmetrics.TaskIDFromContext(ctx)`.
- Where it is set:
  - Store (first pass): `supernode/services/cascade/adaptors/p2p.go` wraps `StoreBatch` calls.
  - Download: `supernode/services/cascade/download.go` wraps `BatchRetrieve` call.

---

## Building and Emitting Events

- Store
  - `supernode/services/cascade/helper.go::emitArtefactsStored(...)`
    - Builds `store` payload via `p2pmetrics.BuildStoreEventPayloadFromCollector(taskID)`.
    - Emits the event.

- Download
  - `supernode/services/cascade/download.go`
    - Builds `retrieve` payload via `p2pmetrics.BuildDownloadEventPayloadFromCollector(actionID)`.
    - Emits the event.

---

## Quick File Map

- Capture + grouping: `pkg/p2pmetrics/metrics.go`
- Store adaptor: `supernode/services/cascade/adaptors/p2p.go`
- Store event: `supernode/services/cascade/helper.go`
- Download flow: `supernode/services/cascade/download.go`
- DHT store calls: `p2p/kademlia/dht.go::IterateBatchStore`
- DHT retrieve calls: `p2p/kademlia/dht.go::BatchRetrieve` and `iterateBatchGetValues`

---

## Notes

- No P2P stats/snapshots are used to build events.
- No aggregation is performed; we only group raw RPC attempts by IP.
- First‑pass success rate is enforced internally (75% threshold) but not emitted as a metric.
