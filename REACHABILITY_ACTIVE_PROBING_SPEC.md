# Reachability + Active Probing Spec (Supernode)

## Scope

- Services/ports in scope:
  - gRPC (default `4444`)
  - P2P (default `4445`)
  - Gateway HTTP status (default `8002`, endpoint `/api/v1/status`)
- Address family: **IPv4 only** (IPv6 is ignored everywhere in reachability and probing).
- No self-connect / hairpin fallback.
- Output is `open_ports` as a per-port state: `OPEN`, `CLOSED`, or `UNKNOWN`.

## Goals

- Make port reachability evidence-based (“reachable from outside” means we observed real inbound traffic).
- Add **active probing** so low-traffic but healthy nodes still get inbound traffic and can be marked `OPEN`.
- Make the probing layer robust enough that **silence can be treated as `CLOSED`** (with explicit quorum rules).
- Keep Lumera as “self-reported metrics”; do not introduce peer-submitted on-chain reachability reports.

## Non-goals

- IPv6 reachability.
- Inferring `OPEN` from local checks (self-dial) or from outbound connectivity.

## Definitions

- **Inbound evidence**: a timestamp recorded when the supernode receives a real inbound IPv4 connection/request on a service.
- **Evidence window**: how long inbound evidence is considered “fresh” for reporting.
- **Epoch**: a chain-derived time window used to schedule probing deterministically across nodes.
- **Eligible set**: the set of supernodes allowed to participate in probing for an epoch.

## 1) Passive evidence (truth source)

### Evidence store

Maintain `last_inbound_time[service]` in-memory, keyed by `{grpc, p2p, gateway}`.

Evidence acceptance rules:

- A remote address must be present.
- Remote address must be **IPv4**.
- Ignore loopback addresses.
- If an authenticated remote identity is available and equals our own identity, ignore it (prevents “self” evidence).

### Evidence capture points

Evidence must be recorded only from real inbound traffic:

- **gRPC**: record on every inbound RPC (unary + streaming) via a server interceptor.
- **Gateway**: record on successful (2xx) `/api/v1/status` responses.
- **P2P**: record only after the secure handshake succeeds.

Rationale: each capture point aligns evidence with the “port is reachable from outside” claim.

## 2) Active probing (traffic generator)

Active probing exists solely to create reliable inbound evidence across the network.

### Participation rules (who probes)

- Probing runs on every **eligible ACTIVE** supernode (mandatory for coverage).
- Eligibility requires a routable IPv4 registration address (private/loopback only in integration tests).
- **POSTPONED** nodes are excluded from the probing budget by default (they may be offline and would dilute coverage for ACTIVE nodes).

### Target rules (who gets probed)

- Targets are **ACTIVE** supernodes only.
- Targets must have a routable IPv4 address from chain registration.
- Private/loopback targets are only allowed in integration tests.

### Epoch and deterministic scheduling

To make “silence means closed” defensible, probe scheduling must be deterministic and consistent:

1. Compute an **epoch id** from chain height:
   - `epoch_id = floor(current_height / epoch_blocks)`
2. Build the eligible set for that epoch:
   - Query `ListSuperNodes()` at (or near) `epoch_id` start.
   - Filter to ACTIVE with a routable IPv4 registration address.
   - Sort by `supernode_account` (stable ordering) to form an indexable list `L` of size `N`.
3. Derive `K` distinct probe offsets from a hash of `(epoch_id, j)`:
   - For `j in [0..K-1]`: `offset_j ∈ [1..N-1]`
4. For each node at index `i` in `L`, its outbound targets are:
   - `target_index = (i + offset_j) mod N`

Properties:

- For a fixed `offset_j`, the mapping is a permutation: each node is targeted exactly once for that `j`.
- Using `K` distinct offsets yields **K distinct probers per target per epoch**.

### Probes executed per target

When probing a target, attempt all three services:

1. **gRPC probe**: connect with ALTS + call gRPC health `Check` (requires `SERVING`).
2. **Gateway probe**: `GET http://<ip>:8002/api/v1/status` (requires 2xx).
3. **P2P probe**: raw TCP dial + Lumera secure handshake.

Why P2P uses “raw TCP dial + handshake”:

- P2P is not gRPC; it is a custom protocol on top of TCP.
- The secure handshake is required to make the connection meaningful and to match when the target records inbound P2P evidence (after handshake success).

### Timeouts and load control

- Each probe has a strict timeout (`ProbeTimeoutSeconds`).
- Probes should be time-distributed within the epoch to avoid synchronized spikes.
- Concurrency must be bounded (implementation detail) to avoid resource exhaustion.

## 3) Port state derivation (OPEN / CLOSED / UNKNOWN)

For each service port, compute:

1. `OPEN` if inbound evidence is fresh within the evidence window.
2. `CLOSED` if inbound evidence is not fresh **and** the probing quorum rules below are satisfied.
3. `UNKNOWN` only if quorum cannot be established (bootstrap conditions), e.g.:
   - not enough eligible peers,
   - chain unreachable / no recent peer list,
   - epoch alignment unavailable.

## 4) Quorum rules (when silence implies CLOSED)

To treat “no inbound evidence” as “closed”, we require proof that the network *should* have produced inbound attempts.

For a node `X` in epoch `E`:

- `AssignedProbers(E, X)` is the set of `K` distinct peers that should probe `X` under the deterministic schedule.
- A prober `P` is considered **alive in epoch E** if chain data shows it has reported metrics recently (e.g., `SuperNode.Metrics.Height` is within the epoch / freshness bounds).

Rule:

- If `alive_assigned_probers(E, X) >= quorum` and `X` has **no inbound evidence** for a service within the evidence window, then the service is `CLOSED`.

This makes “silence means closed” an explicit, checkable condition rather than an assumption.

## Parameters

- `EvidenceWindowSeconds` (default 600s)
- `ProbeTimeoutSeconds` (default 5s)
- `epoch_blocks` (chain-height derived epoch length)
- `K` (probe assignments per epoch, must be ≥ quorum)
- `quorum` (minimum alive assigned probers required to treat silence as `CLOSED`)
