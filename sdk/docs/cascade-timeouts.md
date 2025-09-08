# Cascade Timeouts — Quick Guide

Concise overview of timeout locations, defaults, and intent.

## Defaults

- Register (client → server)
  - Upload (SDK adapter): 60m — `cascadeUploadTimeout`
  - Processing (SDK adapter): 10m — `cascadeProcessingTimeout`
  - Server envelope: 75m — `RegisterTimeout`

- Download (server → client)
  - Server preparation: 5m — `DownloadPrepareTimeout`
  - Client per‑attempt: 60m — `downloadTimeout`
  - Client liveness (SDK adapter):
    - Prep idle (pre‑first‑message): 6m — `downloadPrepIdleTimeout`
    - Idle (post‑first‑message): 2m — `downloadIdleTimeout`
    - Max attempt: 60m — `downloadMaxTimeout`
  - Note: File streaming is not server‑bounded; the client governs transfer.

- Discovery / Connect
  - Health probe per supernode: 10s — `connectionTimeout`
  - gRPC connect default: 30s if caller provides no deadline
  - Keepalives: permissive (idle ping ~1h, ack timeout ~30m)

## Intent and Ordering

- Register: server envelope (75m) > SDK phases (60m + 10m) so the client surfaces errors first when appropriate.
- Download: server prep is tight (5m). Transfer is governed by the client with a generous per‑attempt window and two‑phase idle watchdogs.

## Where They Live

- SDK adapter (upload/download phases): `sdk/adapters/supernodeservice/timeouts.go`
- SDK task (discovery, per‑attempt download): `sdk/task/timeouts.go`
- Supernode service (server envelopes): `supernode/services/cascade/timeouts.go`
- P2P internal RPCs: `p2p/kademlia/network.go` (fixed per‑message timeouts)

## Notes

- Health checks use a 10s budget for snappy discovery.
- gRPC connect/handshake defaults remain unchanged.

## Events (SDK)
- Upload timeout → `SDKUploadFailed`
- Processing timeout → `SDKProcessingTimeout`
- Download failure (timeout/canceled) → `SDKDownloadFailure`

This approach requires no request‑struct changes and preserves existing call sites. It uses a single cancelable context across both phases and phase‑specific timers.

## Minimal Tuning Guidance
- Slow client links: keep download attempt at 60m; adjust idle windows if needed.
- Very large inputs: raise `cascadeUploadTimeout` (keep processing modest at 10m).

## Reference Map
- SDK: `sdk/task/timeouts.go`, `sdk/adapters/supernodeservice/timeouts.go`, `sdk/adapters/supernodeservice/adapter.go`
- Server: `supernode/services/cascade/timeouts.go`, server handlers in `supernode/node/action/server/cascade`
- Network: `pkg/net/grpc/client`, `p2p/kademlia/network.go`

## File/Code Reference Map

- SDK
  - `supernode/sdk/action/client.go` — entrypoints, no timeouts added.
  - `supernode/sdk/task/manager.go` — detaches from caller context; creates and runs tasks.
  - `supernode/sdk/task/timeouts.go` — `connectionTimeout` for health checks.
  - `supernode/sdk/task/task.go` — discovery + health checks using `connectionTimeout`.
  - `supernode/sdk/adapters/supernodeservice/timeouts.go` — upload/processing timeout constants.
  - `supernode/sdk/adapters/supernodeservice/adapter.go` — upload and progress stream handling (phase timers + events).
  - `supernode/sdk/net/factory.go` — client options tuned for streaming.
  - `supernode/pkg/net/grpc/client` — connect timeout injection, readiness wait, retries, keepalive.
  - `supernode/pkg/net/credentials/alts/handshake` — 30s handshake timeouts.

- Supernode
  - `supernode/supernode/node/supernode/server/server.go` — server options (16MB caps, windows, 20 streams).
  - `supernode/supernode/node/action/server/cascade/cascade_action_server.go` — server-side handlers.
  - `supernode/supernode/services/cascade/timeouts.go` — Register (`RegisterTimeout = 75m`) and Download prep (`DownloadPrepareTimeout = 5m`) timeouts.

## Events

- Upload phase timeout: classified as `SDKUploadFailed` with message suffix `| reason=timeout`.
- Processing phase timeout: `SDKProcessingTimeout`.
# Cascade Registration Timeouts and Networking

This document describes how the SDK applies timeouts and deadlines during cascade registration and download, and summarizes the relevant client and server networking defaults.

## Time Budgets

- Upload (adapter): `cascadeUploadTimeout = 60m` — client-side streaming of file chunks and metadata.
- Processing (adapter): `cascadeProcessingTimeout = 10m` — wait for server progress and final tx hash after upload completes.
- Discovery (task): `connectionTimeout = 10s` — per-supernode health probe during discovery.
- Download (task): `downloadTimeout = 60m` — per-attempt envelope. Adapter adds
  `downloadPrepIdleTimeout = 6m` (pre-first-message), `downloadIdleTimeout = 2m`
  (post-first-message), and `downloadMaxTimeout = 60m`.
- gRPC client connect: adds a `30s` deadline if none is present; readiness wait per attempt `ConnWaitTime = 10s` with retries and backoff.
- ALTS handshake: internal `30s` read timeouts on both client and server sides.
- Supernode gRPC server: task-level timeouts are applied (Register 75m). Download preparation is bounded to 5m; file streaming is client-governed. Keepalive is permissive (idle ping ~1h, ack timeout ~30m); flow-control and message-size tuning supports 4MB chunks.

## Control Flow

1) `sdk/action/client.go: ClientImpl.StartCascade(ctx, ...)` — forwards `ctx` to the Task Manager.
2) `sdk/task/manager.go: ManagerImpl.CreateCascadeTask(...)` — detaches from caller (`context.WithCancel(context.Background())`).
3) `sdk/task/cascade.go: CascadeTask.Run(ctx)` — validates file size, discovers healthy supernodes, attempts registration.
4) `sdk/task/task.go: BaseTask.fetchSupernodes` → `BaseTask.isServing` — health probe with `connectionTimeout = 10s` per node.
5) `sdk/task/cascade.go: attemptRegistration` — creates client and calls `RegisterCascade` with task context.
6) `sdk/adapters/supernodeservice/adapter.go: CascadeSupernodeRegister` — applies phase timers:
   - Upload phase: send chunks and metadata; cancel if `cascadeUploadTimeout` elapses.
   - Processing phase: receive server progress and final tx hash; cancel if `cascadeProcessingTimeout` elapses.

## Events

- Upload phase timeout — emitted as `SDKUploadFailed` with `reason=timeout` in the message and `KeyMessage = "timeout"`.
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
