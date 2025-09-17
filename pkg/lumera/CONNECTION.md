# Lumera gRPC Connection Design

This document explains how `pkg/lumera/connection.go` establishes and maintains a robust, long‑lived gRPC client connection.

## Goals

- Be resilient to varying deployment setups (LBs, custom ports, TLS/plain).
- Prefer a fast, successful connection without relying on URL scheme.
- Keep the connection alive for long periods (days) safely.
- Fail fast at startup if no candidate becomes READY.
- Exit the process if a previously established connection is irrecoverably lost.

## High‑Level Flow

1. Parse input into `host` and optional `port` (ignore scheme entirely).
2. Generate dial candidates across ports and security modes:
   - If no port: use default ports `[9090, 443]`.
   - Always generate both TLS and plaintext candidates (4 total when no port; 2 total when a port is specified).
3. Race all candidates concurrently.
4. Each candidate dials non‑blocking, then explicitly waits until the connection reaches `READY` (or times out).
5. The first candidate to hit `READY` is selected; all others are closed (including “late winners”).
6. A monitor goroutine observes the connection state and exits the process if the connection is lost.

## Inputs and Parsing

- `parseAddrMeta(raw string)` extracts `host` and `port`.
- URL schemes (e.g., `https`, `grpcs`, `http`, `grpc`) are ignored for policy decisions. Only host/port matter.
- If no port is provided, we consider it “unspecified” and will try defaults.

## Candidate Generation

- `generateCandidates(meta)` creates a de‑duplicated set of `(target, useTLS)` pairs:
  - Ports:
    - explicit port → `[port]`
    - no port → `defaultDialPorts = [9090, 443]`
  - Security:
    - Always generates both TLS and plaintext for the chosen ports.
- This yields:
  - No scheme + no port: 4 candidates (TLS/PLAIN × 9090/443)
  - Any input with explicit port: 2 candidates (TLS/PLAIN on that port)

Note: TLS creds use `credentials.NewClientTLSFromCert(nil, serverName)`; plaintext uses `insecure.NewCredentials()`.

## Dialing and Readiness

- `createGRPCConnection` uses `grpc.NewClient(target, opts...)` and then:
  - `conn.Connect()` to begin dialing
  - waits in a loop until:
    - `Ready` → success; return the connection
    - `Shutdown`/`TransientFailure` → close and return error
    - idle/connecting → wait for state change with a per‑attempt timeout
- Timeouts:
  - `dialReadyTimeout` (default 10s) applies if the provided context has no deadline.

## Selecting the Winner

- `newGRPCConnection` starts all candidate attempts and collects results on a buffered channel.
- The first `READY` result becomes the winner; we keep receiving remaining results to explicitly close any non‑winners (including late winners) to avoid leaks.
- All pending attempts are canceled via a parent context cancellation.

## Blocking Behavior

- The constructor returns only after a connection is in `READY` state.
- If all candidates fail, it returns an error (startup abort).

## Keepalive for Long‑Lived Connections

- Client keepalive parameters are tuned for Cosmos‑SDK environments:
  - `keepaliveIdleTime = 10m` (send pings no more than every 10 minutes when idle)
  - `keepaliveAckTimeout = 20s`
  - `PermitWithoutStream = true`
- Rationale:
  - Conservative ping interval avoids server `GOAWAY` for “too_many_pings”.
  - Keeps NAT/firewalls from silently expiring idle connections.

## Connection Monitor and Process Exit

- `monitorConnection` watches the connection state:
  - `Shutdown` → log and `os.Exit(1)`
  - `TransientFailure` → allow up to `reconnectionGracePeriod = 30s` for recovery; if not recovered, log and exit.
- Normal reconnection behavior is handled by gRPC; the monitor only exits if the connection does not recover within the grace period or is shut down definitively.

## Logging

- On success, we log: target (`host:port`) and scheme (`tls` or `plaintext`).
- Errors during attempts surface as aggregated failure if no candidate succeeds.

## Thread‑Safety

- The only map (`seen`) is local to candidate generation and used single‑threaded.
- Concurrency is limited to goroutines dialing candidates and sending results through a channel; no shared maps are mutated concurrently.
- Late winner cleanup explicitly closes extra connections to avoid resource leaks.

## Configuration Knobs (Constants)

- `defaultDialPorts = [9090, 443]`
- `dialReadyTimeout = 10s` (per attempt, if no deadline present)
- `keepaliveIdleTime = 10m`
- `keepaliveAckTimeout = 20s`
- `reconnectionGracePeriod = 30s`

## Extensibility

- Ports: Adjust `defaultDialPorts` if your environment prefers a different port set.
- TLS: To support custom roots or mTLS, add an option to inject `TransportCredentials` instead of the defaults.
- Policies: If future schemes or resolvers are introduced, they can be layered in before candidate generation.

## Error Cases and Behavior

- If no candidate reaches `READY` within `dialReadyTimeout` per attempt, `newGRPCConnection` returns an error.
- If the connection later enters a prolonged `TransientFailure` or `Shutdown`, the monitor exits the process.

## FAQ

- Why try both TLS and plaintext? We avoid making assumptions based on scheme and instead race practical permutations to maximize robustness across deployments.
- Why include both 9090 and 443? These are common in gRPC deployments (custom service ports and TLS‑terminating LBs). Adjust as needed for your infra.
- Does this support Unix sockets? Not currently; could be added by extending candidate generation.

