# Reachability + Active Probing (Supernode)

This document describes how the supernode derives `open_ports` and how the
network-wide active probing layer works.

For the design rationale and spec, see `REACHABILITY_ACTIVE_PROBING_SPEC.md`.

## Overview

The supernode reports reachability as `open_ports`, where each port is one of:

- `OPEN`: we observed **real inbound IPv4 traffic** recently.
- `CLOSED`: we have **no recent inbound evidence** and the probing quorum rules
  say that “silence is meaningful”.
- `UNKNOWN`: we cannot establish quorum (bootstrap / chain unavailable / not
  enough eligible peers).

Key properties:

- **IPv4 only**: IPv6 is ignored everywhere.
- **Evidence-based**: no self-dials and no local-only checks are used to mark
  a port `OPEN`.
- **Active probing** exists only to generate inbound traffic so quiet-but-healthy
  nodes still receive real connections.

## Passive inbound evidence (truth source)

Inbound evidence is stored in-memory as `last_inbound_time[service]`:

- gRPC (`ServiceGRPC`)
- P2P (`ServiceP2P`)
- Gateway status (`ServiceGateway`)

Notes:

- The evidence store is **in-memory only** and is reset on process restart.
  Expect ports to start at `UNKNOWN` after restart until new inbound evidence is observed.
- Evidence is tracked per *service*, then applied to the configured port for that service when
  building `open_ports`.

Evidence is accepted only when:

- A remote network address exists.
- The remote address is **IPv4**.
- The remote address is not loopback.
- If an authenticated remote identity is available and equals our own identity,
  it is ignored (prevents “self” evidence).

### Capture points

- **gRPC**: recorded for every inbound RPC via gRPC interceptors.
  - Code: `pkg/net/grpc/server/server.go`
- **Gateway**: recorded on successful (2xx) `/api/v1/status` responses.
  - Code: `supernode/transport/gateway/server.go`
- **P2P**: recorded only after the secure handshake succeeds.
  - Code: `p2p/kademlia/network.go`, `p2p/kademlia/conn_pool.go`

The store implementation lives in `pkg/reachability/`, and is initialized at
process start in `supernode/cmd/start.go`.

## Active probing (traffic generator)

Active probing generates a small amount of inbound traffic so that evidence can
be refreshed even on low-traffic nodes.

### Participation / targets

- Probing runs on **ACTIVE** supernodes.
- Targets are **ACTIVE** supernodes with a **routable IPv4** address from chain
  registration.
- Private/loopback targets are only allowed when `INTEGRATION_TEST=true`.

### Epoch scheduling (deterministic)

To make “silence means closed” defensible, probe assignments are deterministic.

Inputs:

- `epoch_blocks`: derived from the chain param `metrics_update_interval_blocks`
- `epoch_id = floor(current_height / epoch_blocks)`
- Eligible set `L`: ACTIVE supernodes with routable IPv4, sorted by
  `supernode_account`
- `N = len(L)`
- `K = ProbeAssignmentsPerEpoch` (default 3)

Offsets:

- `K` distinct offsets are derived from a stable hash of `(epoch_id, j)` and
  mapped into `[1..N-1]`.

Assignments:

- For a node at index `i` in `L`, its outbound targets are:
  - `target_index = (i + offset_j) mod N` for `j in [0..K-1]`

This yields **K distinct probers per target per epoch**.

Implementation:

- Scheduling and probing loop: `supernode/supernode_metrics/active_probing.go`

### Probe execution per target

Each target is probed on all three services (with strict timeouts):

1. **gRPC**: ALTS connection + gRPC health `Check` must be `SERVING`.
2. **Gateway**: `GET http://<ip>:8002/api/v1/status` must be 2xx.
3. **P2P**: TCP dial + Lumera secure handshake must succeed.

The probing loop uses jitter to avoid synchronized spikes.
It also spaces probes across the epoch by waiting roughly `reportInterval / K`
between outbound probes (with jitter).

## Port state derivation (`open_ports`)

For each local service port:

1. `OPEN` if inbound evidence is fresh within the evidence window.
2. `CLOSED` if evidence is not fresh **and** the quorum rule is satisfied.
3. `UNKNOWN` otherwise.

### Evidence window

The freshness window is:

- `max(EvidenceWindowSeconds, 2 * reportInterval)`

This prevents evidence from expiring between expected metrics reports.

## Quorum rule (when silence implies CLOSED)

For node `X` in epoch `E`:

- `AssignedProbers(E, X)` is the set of `K` peers that should probe `X` under
  the deterministic schedule.
- A prober is “alive” if its on-chain `SuperNode.Metrics.Height` is:
  - within the current epoch, and
  - within `metrics_freshness_max_blocks` of the current height.

If `alive_assigned_probers(E, X) >= ProbeQuorum` (default 2) and there is no
fresh inbound evidence for a service, that service is reported as `CLOSED`.

Implementation:

- Quorum calculation: `supernode/supernode_metrics/reachability_quorum.go`

## Operational behavior

### When ports become OPEN

A port becomes `OPEN` only after the node observes inbound IPv4 traffic for the
corresponding service:

- gRPC: any inbound RPC (including health checks)
- Gateway: a successful (2xx) request to `/api/v1/status`
- P2P: a successful secure handshake on the P2P socket

In low-traffic environments, `OPEN` typically comes from the active probing
layer, which generates a small amount of deterministic inbound traffic.

### When ports become CLOSED

A port becomes `CLOSED` only when:

- there is no fresh inbound evidence in the evidence window, and
- `alive_assigned_probers >= ProbeQuorum` for the current epoch

If quorum cannot be established (e.g. not enough eligible peers, chain is
unreachable, or peers are not reporting metrics), the port remains `UNKNOWN`
instead of being inferred closed.

## Debugging

- Active probing logs are emitted at `DEBUG` level with prefix `Active probing: ...`.
- Metrics reporting logs include `open_ports` in `supernode/supernode_metrics/monitor_service.go`.

## Configuration knobs

Defaults are compile-time constants in `supernode/supernode_metrics/constants.go`:

### Core constants

- `ProbeTimeoutSeconds` (default 5s)
  - Upper bound for a single outbound probe to a peer (gRPC dial + health check, HTTP status call, P2P dial + handshake).
  - If your network has high latency or slow handshakes, increase this; keep it small to avoid hanging goroutines.
- `ProbeAssignmentsPerEpoch` (default 3)
  - “How many distinct peers should probe each node per epoch” (K in the spec).
  - Increasing this increases inbound traffic and improves confidence, especially in flaky networks.
- `ProbeQuorum` (default 2)
  - “How many of the assigned probers must be alive” before we allow inferring `CLOSED` from silence.
  - Must satisfy `ProbeAssignmentsPerEpoch >= ProbeQuorum`.
- `MinProbeAttemptsPerReportInterval` (default 3)
  - Minimum number of outbound *peer probes* attempted per metrics reporting interval.
  - If the deterministic schedule yields fewer outbound targets than this (e.g. very small networks), probing will wrap and probe targets multiple times per interval to ensure at least this many attempts.
- `EvidenceWindowSeconds` (default 600s)
  - Minimum freshness window for inbound evidence.
  - The effective window used for `OPEN` is: `max(EvidenceWindowSeconds, 2 * reportInterval)`.

### Ports

- `APIPort` (default 4444), `P2PPort` (default 4445), `StatusPort` (default 8002)
  - Defaults used in `open_ports` reporting; should stay aligned with the chain `required_open_ports` param.

Chain-derived inputs:

- `metrics_update_interval_blocks` (used for `epoch_blocks` and `reportInterval`)
- `metrics_freshness_max_blocks` (used for prober “alive” checks)

## Tuning examples

### Example A: report interval = 30s

- `MinProbeAttemptsPerReportInterval=3` means each node attempts at least 3 peer probes per 30s interval.
  - If `ProbeAssignmentsPerEpoch=3`, the normal behavior is ~1 probe every 10s (plus jitter).
  - If the eligible set is tiny and yields only 1 outbound target, the node still probes ~every 10s, wrapping to the same target.
- Evidence freshness window becomes: `max(600s, 2*30s) = 600s`.

### Example B: report interval = 5m

- With `ProbeAssignmentsPerEpoch=3`, a node probes ~once every `5m/3 ≈ 100s` (plus jitter).
- Evidence freshness window becomes: `max(600s, 2*5m) = 10m`.

### Example C: “honest-but-flaky” network

Goal: reduce false `CLOSED` caused by missed probe attempts.

- Consider increasing to `ProbeAssignmentsPerEpoch=5` and `ProbeQuorum=3`.
- Consider increasing `EvidenceWindowSeconds` if brief outages are common, so nodes don’t lose `OPEN` too quickly.

## Testing

- Unit tests: `go test ./supernode/supernode_metrics`
- Integration environments can allow probing private IPv4 targets by setting:
  - `INTEGRATION_TEST=true`
