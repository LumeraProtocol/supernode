# P2P Replication + Routing Eligibility Changes (2026-01-13)

This document describes all currently uncommitted changes in the `supernode` repository that affect the P2P/Kademlia subsystem, including the replication/backlog fixes and routing-table eligibility gating.

## Context / Symptoms Observed

These changes were prompted by two production symptoms:

1. P2P metrics error (status/telemetry path):
   - Log: `failed to get p2p records count`
   - Error: `failed to get count of records: context deadline exceeded`
   - Typical attributes included `host=<supernode identity>`, `service=<ip:port>`

2. Replication producing huge key payloads (replication worker path):
   - Log: `replicate batching keys`
   - Example attributes:
     - `batch_size=5000`
     - `keys=15934902`
     - `batches=3187`
     - `correlation_id=supernode-start`
     - `module=p2p`
     - `rep-id=<peer supernode account>`
     - `rep-ip=<peer ip>`

Historically, replication had a bug that prevented progress when payloads became oversized; batching fixed request size, but exposed/created a large backlog catch-up scenario where a single replication cycle attempted to send millions of keys.

## High-Level Outcomes

The combined changes aim to:

- Make replication “catch-up” bounded and incremental (steady progress instead of huge one-shot attempts).
- Reduce replication complexity (avoid per-peer re-scans of the same key set).
- Advance replication cursors safely (avoid skipping keys when operating in bounded windows).
- Reduce noisy error logging for best-effort DB metrics under load.
- Gate routing table membership to chain-active supernodes (avoid non-active peers participating in routing and replication decisions).

## Change Inventory (Files Modified)

The following files are modified:

- `p2p/kademlia/bootstrap.go`
- `p2p/kademlia/dht.go`
- `p2p/kademlia/node_activity.go`
- `p2p/kademlia/redundant_data.go`
- `p2p/kademlia/replication.go`
- `p2p/kademlia/store.go`
- `p2p/kademlia/store/mem/mem.go`
- `p2p/kademlia/store/sqlite/replication.go`
- `p2p/kademlia/store/sqlite/sqlite.go`

## Detailed Changes

### 1) Routing table eligibility gating (chain-active only)

#### `p2p/kademlia/bootstrap.go`

**What changed**

- `loadBootstrapCandidatesFromChain(...)` now returns two values:
  1) `map[string]*Node` candidates keyed by `ip:port`
  2) `map[[32]byte]struct{}` allowlist of *active* supernode IDs, stored as `blake3(supernodeAccount)` keys
- The supernode account string is trimmed, validated non-empty, and used consistently as `node.ID`.
- `SyncBootstrapOnce(...)` now:
  - calls `setRoutingAllowlist(...)` with the active ID hash-set
  - calls `pruneIneligibleRoutingPeers(...)` to remove already-admitted peers that are now ineligible

**Why**

- Kademlia routing decisions (closest nodes) should only consider chain-active supernodes. Without this, postponed/disabled/stopped nodes can:
  - be admitted via inbound traffic
  - appear in `FindNodeResponse.Closest`
  - skew replication responsibility calculations

#### `p2p/kademlia/dht.go`

**What changed**

- Added an in-memory allowlist gate to `DHT`:
  - `routingAllow map[[32]byte]struct{}`
  - `routingAllowReady atomic.Bool`
  - `routingAllowCount atomic.Int64`
  - guarded by `routingAllowMu`
- New helpers:
  - `setRoutingAllowlist(ctx, allow)`:
    - refuses empty allowlists (to avoid locking out due to transient chain issues)
    - no-ops when `INTEGRATION_TEST=true`
  - `eligibleForRouting(node)`:
    - returns true when allowlist isn’t ready (bootstrap safety)
    - returns true when `INTEGRATION_TEST=true`
    - otherwise checks `blake3(node.ID)` membership in the allowlist
  - `filterEligibleNodes(nodes)` filters node slices returned by the network (FindNode/FindValue paths)
  - `pruneIneligibleRoutingPeers(ctx)` walks the current routing table and removes ineligible nodes
- Integrated eligibility checks into:
  - iterative lookup handling: `nl.AddNodes(s.filterEligibleNodes(v.Closest))`
  - response handling for `FindNode`/`StoreData`/`FindValue`
  - `addNode(...)`:
    - early nil/empty-ID returns
    - invalid IP rejection log level reduced to Debug
    - chain-state gating: rejects peers not in allowlist
  - `addKnownNodes(...)`:
    - skips nil/empty-ID nodes
    - skips nodes failing `eligibleForRouting(...)`

**Why**

- Prevent non-active peers from entering buckets, appearing in closest-sets, or being considered as replication targets.
- Keep performance acceptable by keeping gating as a fast memory lookup, updated on the bootstrap refresh cadence.

#### `p2p/kademlia/node_activity.go`

**What changed**

- Node activity checks now skip pinging/promoting nodes that are not eligible for routing.
- If a node is currently marked Active but becomes ineligible:
  - it is removed from routing (`removeNode`)
  - replication info is updated to inactive via `store.UpdateIsActive(..., false, false)`
- `handlePingSuccess` now also refuses to “promote” an ineligible node to active/routing.

**Why**

- Avoid spending cycles on peers that the chain says should not participate.
- Avoid “re-activating” a peer solely due to network reachability when it’s ineligible by chain state.

### 2) Replication backlog + oversized payload mitigation (bounded windows + one-pass assignment)

#### Root-cause (before)

- Replication used a time window `[globalFrom, now]` and fetched *all* keys from sqlite in that window.
- For each peer, it then:
  - found the slice index for that peer’s `lastReplicatedAt` within the global key list, then
  - iterated over all remaining keys to filter “keys this peer should hold”.
- After batching was added, a peer could still be responsible for millions of keys, which became thousands of batch RPCs.
- Cursor update (`lastReplicatedAt`) happened only after all batches succeeded; a single failure in thousands of batches meant zero progress and repeated attempts next cycle.

#### `p2p/kademlia/store.go`

**What changed**

- Store interface method signature changed:
  - from: `GetKeysForReplication(ctx, from, to)`
  - to: `GetKeysForReplication(ctx, from, to, maxKeys int)` where `maxKeys <= 0` means “unlimited”.

**Why**

- Allows callers (replication worker) to bound the per-cycle key scan to a manageable size.

#### `p2p/kademlia/store/sqlite/replication.go`

**What changed**

- Added support for bounded key scans:
  - Base query now orders deterministically: `ORDER BY createdAt ASC, key ASC`
  - When `maxKeys > 0`, a `LIMIT ?` is applied.
  - If the limit is hit, the query is extended to include all additional rows where `createdAt` equals the last row’s `createdAt` (by fetching `createdAt = boundAt AND key > boundKey`).
- `ctx.Err()` is checked post-query; cancellation/deadline is treated as failure (returns nil).

**Why**

- Ordering by `(createdAt, key)` provides a stable cursor for a bounded window.
- Including “same `createdAt`” rows at the limit boundary prevents the replication cursor from skipping keys that share the same timestamp.

#### `p2p/kademlia/store/mem/mem.go`

**What changed**

- Updated `GetKeysForReplication` signature to match interface (`maxKeys` unused).

#### `p2p/kademlia/replication.go`

**What changed**

- Introduced `replicateKeysScanMax = 200000` to bound the per-cycle DB scan.
- Replication now fetches:
  - `replicationKeys := store.GetKeysForReplication(globalFrom, to, replicateKeysScanMax)`
- Defines a stable replication “window end”:
  - `windowEnd = lastKey.CreatedAt` if keys exist, else `windowEnd = to`
- Replaces per-peer filtering loops with a one-pass assignment:
  - Build `peerStart[peerID] = lastReplicatedAt (or historicStart)`
  - For each key:
    - compute `closestIDs := closestContactsWithIncludingNode(Alpha, key, ignores, self)`
    - for each closest ID that is an active peer, if `key.CreatedAt.After(peerStart[id])`, append key into `assignedKeys[id]`
- Each active peer then:
  - sends `assignedKeys[peerID]` in 5000-key batches (existing batching)
  - on full success, advances `lastReplicatedAt` to `windowEnd` (not `time.Now()`)
  - if no keys assigned for that peer, still advances `lastReplicatedAt` to `windowEnd`
- `adjustNodeKeys` switched to the new `GetKeysForReplication` signature (unbounded).

**Why**

- Bounding the scan turns replication into incremental progress rather than an unbounded backlog dump.
- `windowEnd` ensures cursor advancement is aligned to what was actually scanned/processed.
- One-pass assignment reduces CPU/memory pressure from O(peers * keys) scanning to O(keys * Alpha) assignment.

**Operational expectations**

- Nodes with large backlogs will now “catch up” over multiple intervals (e.g., 16M keys at 200k keys/cycle ≈ 80 cycles).
- Per-interval replication logs should show materially smaller `keys` and `batches` counts.

### 3) Redundant data cleanup worker compatibility

#### `p2p/kademlia/redundant_data.go`

- Updated call signature for `GetKeysForReplication(..., 0)` to mean “unlimited” in the redundant-data cleanup path.

### 4) P2P DB stats logging noise reduction

#### `p2p/kademlia/store/sqlite/sqlite.go`

**What changed**

- When `Store.Count(ctx)` fails inside `Store.Stats(ctx)`:
  - If the error is `context.DeadlineExceeded` or `context.Canceled`, it logs at Debug instead of Error.
  - Other errors remain Error.

**Why**

- In production the count query (`SELECT COUNT(*) FROM data`) can legitimately time out under heavy DB load or when metrics collection has a short deadline.
- The count is typically a best-effort metric; logging it as ERROR creates noise and can mask real faults.

## Notes / Known Limitations

- Replication assignment uses the current routing table’s `closestContactsWithIncludingNode(...)`. If an otherwise active peer is not present in routing-table-derived closest sets, it may receive zero keys for a window and still have its `lastReplicatedAt` advanced to `windowEnd`. This behavior existed in the prior approach as well (advancing on “no closest keys”), but bounding makes it more visible as replication progresses window-by-window.
- `replicateKeysScanMax` is a constant. If you need environment-specific tuning, consider making it configurable (config file/env var), but this change intentionally keeps scope minimal.
- The bounded sqlite query may return slightly more than `maxKeys` due to the “same createdAt extension” for correctness.

## Verification Performed

Local targeted unit tests were run for the modified packages:

- `go test ./p2p/kademlia/...` (passes)

Full `go test ./...` may fail in restricted environments due to network/port permissions in integration tests; those failures are unrelated to the code changes described here.

