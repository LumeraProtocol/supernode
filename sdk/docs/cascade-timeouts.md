# Cascade Registration Timeouts and Networking

This document explains how timeouts and deadlines are applied across the SDK cascade registration flow, including the current split between upload and processing phases and the relevant client/server defaults.

## Purpose

- Make slow, user‑network–dependent uploads more tolerant without impacting other stages.
- Keep health checks and connection establishment responsive.
- Enable clearer error categorization: upload vs processing.

## TL;DR Defaults

- Upload timeout (adapter): `cascadeUploadTimeout = 60m` — covers client-side file streaming to the supernode.
- Processing timeout (adapter): `cascadeProcessingTimeout = 10m` — covers waiting for server progress/final tx hash after upload completes.
- Health check to supernodes (task): `connectionTimeout = 10s` — per-node probe during discovery.
- gRPC connect (client):
  - Adds a default `30s` deadline if caller context has none.
  - Connection readiness gate: `ConnWaitTime = 10s` per attempt, with `MaxRetries = 3` and retry backoff.
- ALTS handshake (secure transport): `30s` internal read timeouts (client and server sides).
- Supernode gRPC server:
  - No per‑RPC timeout for `Register`/`Download` handlers.
  - Keepalive is permissive (idle ping at 1h, ping ack timeout 30m).
  - Stream tuning: 16MB message caps, 16MB stream window, 160MB conn window, ~20 concurrent streams.

## Control Flow and Contexts

1) `sdk/action/client.go: ClientImpl.StartCascade(ctx, ...)`
   - Forwards `ctx` to Task Manager.

2) `sdk/task/manager.go: ManagerImpl.CreateCascadeTask(...)`
   - Detaches from caller: `taskCtx := context.WithCancel(context.Background())`.
   - All subsequent work uses `taskCtx` (no deadline by default).

3) `sdk/task/cascade.go: CascadeTask.Run(ctx)`
   - Validates file size; fetches healthy supernodes; registers with one.

4) Discovery: `sdk/task/task.go: BaseTask.fetchSupernodesWithLoads` (single-pass sanitize + load)
   - `context.WithTimeout(parent, 10s)` per node: `HealthCheck` + `GetStatus` (peers, running_tasks) + balance.

5) Registration attempt: `sdk/task/cascade.go: attemptRegistration`
   - Client connect: uses task context (no deadline); gRPC injects a 30s default at connect if needed.
   - No outer registration timeout here; the adapter handles per‑phase timers.

6) RPC staging:
   - `sdk/net/impl.go: supernodeClient.RegisterCascade` →
   - `sdk/adapters/supernodeservice/adapter.go: CascadeSupernodeRegister` performs client‑stream upload and reads server progress / final tx hash.

## Where Timeouts Come From (by Layer)

- SDK adapter level (registration RPC):
  - `cascadeUploadTimeout` (60m): upload phase timer (file chunks + metadata + CloseSend).
  - `cascadeProcessingTimeout` (10m): processing phase timer (receive server progress + final tx hash).
- SDK task level:
  - `connectionTimeout` (10s): supernode health checks only.

- gRPC client (`pkg/net/grpc/client`):
  - `defaultTimeout = 30s`: applied to connect if context lacks a deadline.
  - `ConnWaitTime = 10s`, `MaxRetries = 3`, backoff configured; keepalives: 30m/30m.

- ALTS handshake (`pkg/net/credentials/alts/handshake`):
  - `defaultTimeout = 30s` for handshake read operations (client/server).

- gRPC server (`pkg/net/grpc/server` and supernode runtime):
  - No explicit per‑RPC timeouts; generous keepalives; tuned flow control and message sizes for 4MB chunks.

## SDK Constants

Timeout constants are defined in dedicated files for clarity:

- Upload/Processing: `supernode/sdk/adapters/supernodeservice/timeouts.go`
- Connection/health probe: `supernode/sdk/task/timeouts.go`

Notes:
- `BaseTask.isServing` keeps a short 10s budget for snappy health checks.
- gRPC connect/handshake defaults remain unchanged.

## Implementation Details

The split is implemented inside `CascadeSupernodeRegister` where the phases are naturally separated by the client‑stream CloseSend.

1) Create a cancelable context from the inbound one for the stream lifetime:

```go
phaseCtx, cancel := context.WithCancel(ctx)
defer cancel()
stream, err := a.client.Register(phaseCtx, opts...)
```

2) Upload phase timer:

```go
uploadTimer := time.AfterFunc(cascadeUploadTimeout, cancel)

// send chunks...
// send metadata...

if err := stream.CloseSend(); err != nil { /* ... */ }
uploadTimer.Stop()
```

3) Processing phase timer (server progress → final tx hash):

```go
processingTimer := time.AfterFunc(cascadeProcessingTimeout, cancel)
defer processingTimer.Stop()

for {
    resp, err := stream.Recv()
    // handle EOF, errors, progress, final tx hash
}
```

4) Error mapping and events:
- If cancellation occurs during Send loop → classify as upload timeout and emit `SDKUploadTimeout`.
- If cancellation occurs during Recv loop → classify as processing timeout and emit `SDKProcessingTimeout`.
- Surface distinct error messages and publish events accordingly.

This approach requires no request‑struct changes and preserves existing call sites. It uses a single cancelable context across both phases and phase‑specific timers.

## Additional Notes

- Health checks use `connectionTimeout = 10s` during supernode discovery.
- gRPC client connect behavior: adds a `30s` deadline if none is present, waits up to `ConnWaitTime = 10s` per attempt with retries.
- Downloads use a separate `downloadTimeout = 5m` envelope.

## Operational Guidance

- For slow client links: raise `cascadeUploadTimeout` (e.g., 30–120m). Keep processing modest (e.g., 5–10m) unless chain finalization is known to stall.
- Server tuning is already generous; no server change required to support longer uploads.
- Telemetry: differentiate upload vs processing timeout in logs and emitted events for better retry behavior and user messaging.
- Retry policy: on upload timeout, prefer retrying with a different supernode; on processing timeout, consider whether the server might still finalize (idempotency depends on service semantics).

## File/Code Reference Map

- SDK
  - `supernode/sdk/action/client.go` — entrypoints, no timeouts added.
  - `supernode/sdk/task/manager.go` — detaches from caller context; creates and runs tasks.
  - `supernode/sdk/task/timeouts.go` — `connectionTimeout` for health checks.
  - `supernode/sdk/task/task.go` — discovery with single-pass probe (`fetchSupernodesWithLoads`) using `connectionTimeout`.
  - `supernode/sdk/adapters/supernodeservice/timeouts.go` — upload/processing timeout constants.
  - `supernode/sdk/adapters/supernodeservice/adapter.go` — upload and progress stream handling (phase timers + events).
  - `supernode/sdk/net/factory.go` — client options tuned for streaming.
  - `supernode/pkg/net/grpc/client` — connect timeout injection, readiness wait, retries, keepalive.
  - `supernode/pkg/net/credentials/alts/handshake` — 30s handshake timeouts.

- Supernode
  - `supernode/supernode/node/supernode/server/server.go` — server options (16MB caps, windows, 20 streams).
  - `supernode/supernode/node/action/server/cascade/cascade_action_server.go` — server-side Register/Download handlers (no per‑RPC timeout).

## Events

- Upload phase timeout: `SDKUploadTimeout`.
- Processing phase timeout: `SDKProcessingTimeout`.
# Cascade Registration Timeouts and Networking

This document describes how the SDK applies timeouts and deadlines during cascade registration and download, and summarizes the relevant client and server networking defaults.

## Time Budgets

- Upload (adapter): `cascadeUploadTimeout = 60m` — client-side streaming of file chunks and metadata.
- Processing (adapter): `cascadeProcessingTimeout = 10m` — wait for server progress and final tx hash after upload completes.
- Discovery (task): `connectionTimeout = 10s` — per-supernode health probe during discovery.
- Download (task): `downloadTimeout = 5m` — envelope for cascade download.
- gRPC client connect: adds a `30s` deadline if none is present; readiness wait per attempt `ConnWaitTime = 10s` with retries and backoff.
- ALTS handshake: internal `30s` read timeouts on both client and server sides.
- Supernode gRPC server: no per-RPC timeout; keepalive is permissive (idle ping ~1h, ack timeout ~30m); flow-control and message-size tuning supports 4MB chunks.

## Control Flow

1) `sdk/action/client.go: ClientImpl.StartCascade(ctx, ...)` — forwards `ctx` to the Task Manager.
2) `sdk/task/manager.go: ManagerImpl.CreateCascadeTask(...)` — detaches from caller (`context.WithCancel(context.Background())`).
3) `sdk/task/cascade.go: CascadeTask.Run(ctx)` — validates file size, discovers healthy supernodes, attempts registration.
4) `sdk/task/task.go: BaseTask.fetchSupernodesWithLoads` — single-pass probe with `connectionTimeout = 10s` per node (health, status, balance) and load snapshot.
5) `sdk/task/cascade.go: attemptRegistration` — creates client and calls `RegisterCascade` with task context.
6) `sdk/adapters/supernodeservice/adapter.go: CascadeSupernodeRegister` — applies phase timers:
   - Upload phase: send chunks and metadata; cancel if `cascadeUploadTimeout` elapses.
   - Processing phase: receive server progress and final tx hash; cancel if `cascadeProcessingTimeout` elapses.

## Events

- `SDKUploadTimeout` — emitted when the upload phase exceeds its time budget.
- `SDKProcessingTimeout` — emitted when the post-upload processing exceeds its time budget.

## Files and Constants

- `supernode/sdk/adapters/supernodeservice/timeouts.go` — `cascadeUploadTimeout`, `cascadeProcessingTimeout`.
- `supernode/sdk/adapters/supernodeservice/adapter.go` — phased timers and stream handling.
- `supernode/sdk/task/timeouts.go` — `connectionTimeout` for discovery health checks.
- `supernode/sdk/task/task.go` — discovery and health probing.
- `supernode/sdk/task/download.go` — `downloadTimeout` for downloads.
- `supernode/pkg/net/grpc/client` — connect deadline injection, readiness wait, retries, keepalive defaults.
- `supernode/pkg/net/credentials/alts/handshake` — ALTS handshake timeouts.
- `supernode/supernode/node/supernode/server/server.go` — server stream tuning and keepalive parameters.

## Tuning

Adjust the constants in the SDK to fit deployment requirements (e.g., extend upload timeout for slower networks). Client/server defaults can be tuned as needed while keeping discovery responsive and long uploads reliable.
