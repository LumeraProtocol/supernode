# Self-Healing E2E Test Plan (System Harness)

## Goal

Add deterministic, production-grade system E2E coverage for self-healing in `tests/system`, aligned with the current off-chain phase:

- Trigger based on weighted watchlist (multi-reporter epoch reports).
- Challenge generation from on-chain CASCADE actions.
- Recipient-side reconstruction via `RecoveryReseed`.
- Observer quorum verification.
- Restart-safe once-per-window behavior.

## Out of Scope

- Any on-chain phase-4 capability/governance changes.
- Redesign of `x/audit` or `x/action` module semantics.
- Replacing current supernode runtime architecture.

## Existing Harness Baseline

- Use existing system harness and lifecycle already used by cascade tests:
  - `tests/system/main_test.go`
  - `tests/system/e2e_cascade_test.go`
  - `tests/system/supernode-utils.go`
- Existing startup commands:
  - `make setup-supernodes`
  - `make test-cascade`
  - `make test-e2e`

## Files To Add

- `tests/system/e2e_self_healing_test.go`
- `tests/system/self_healing_helpers.go`

## Optional Harness Hardening (Recommended First)

To ensure truly per-node event-state assertions, isolate node-local SQLite in system tests:

1. In `tests/system/supernode-utils.go`, set per-process `HOME` to each node data dir before `cmd.Start()`.
2. Keep one sqlite DB per supernode process (`$HOME/.supernode/history.db`).

If this is not done, system tests may still pass but assertions that depend on node-local ownership/lease state can be ambiguous.

## Test Fixture Design

Create one reusable fixture function, for example `setupSelfHealingFixture(t)`:

1. Start chain and register supernodes (reuse existing helper flow).
2. Start all supernodes.
3. Create one CASCADE action from `tests/system/test.txt` (reuse cascade helper/client logic).
4. Wait for action to reach `DONE`/`APPROVED`.
5. Capture:
   - `actionID`
   - expected data hash from action metadata (`CascadeMetadata.DataHash`)
   - candidate anchor key from metadata (`RqIdsIds` smallest lexicographic key)

## Weighted Watchlist Trigger Setup

Self-healing trigger requires weighted view, not a single local opinion.

Use `AuditMsg().SubmitEpochReport(...)` from multiple reporter nodes in the same epoch:

1. Query current epoch via `Audit().GetCurrentEpoch`.
2. Query audit params for:
   - `required_open_ports`
   - `peer_quorum_reports`
   - `peer_port_postpone_threshold_percent`
3. Submit reports from at least `peer_quorum_reports` distinct reporters against target holders.
4. For watchers intended to be flagged, make `closed_votes / total_votes >= threshold`.
5. Wait at least one block after report submission.

## Core E2E Scenarios

### 1) Happy Path: Request -> Reseed -> Verify -> Complete

Steps:

1. Ensure holders of the selected action anchor key are on weighted watchlist.
2. Wait for one generation/process window.
3. Let challenger emit challenge and process event.

Assertions:

1. One event exists per `(window, action_id)` challenge ID.
2. Recipient response accepted.
3. `reconstruction_required=true` when recipient was missing local data.
4. Observer verification reaches threshold.
5. Event status becomes `completed`.
6. Reconstructed content hash matches action metadata hash (see hash assertion section).

### 2) Recipient Down -> Retry -> Terminal

Steps:

1. Bring recipient process down before processing.
2. Let challenger process event across retries.

Assertions:

1. Event transitions to `retry` with backoff timestamps.
2. `attempt_count` increments each claim.
3. After `max_event_attempts`, status becomes `terminal`.
4. Terminal reason indicates recipient/request failure.

### 3) Observer Quorum Fail

Steps:

1. Keep recipient up.
2. Make enough observers unavailable or mismatch response.

Assertions:

1. Verification messages captured for observers.
2. `ok_count < observer_threshold`.
3. Event is retried, then terminal when max attempts is reached.

### 4) Duplicate Replay / Once-Per-Window

Steps:

1. Trigger same generation window repeatedly.
2. Attempt duplicate insertion/replay of same challenge ID.

Assertions:

1. Only one row per challenge ID in `self_healing_challenge_events`.
2. Processing runs once for that window challenge identity.
3. No duplicate completion metrics for same sender/message type tuple.

### 5) Restart-Safe Lease Reclaim

Steps:

1. Force challenger to claim event.
2. Kill challenger before completion update.
3. Wait lease expiry.
4. Restart challenger.

Assertions:

1. Event is reclaimed.
2. Processing resumes and completes (or retries/terminal deterministically).
3. No double-complete state.

### 6) Stale Window Handling

Steps:

1. Insert or mutate an old event payload with stale `window_id`.
2. Run event processor tick.

Assertions:

1. Event immediately marked `terminal`.
2. Reason is `stale_window`.
3. No request RPC is sent.

### 7) Scale/Throughput Smoke (Bounded)

Steps:

1. Generate many eligible targets (for system test keep bounded, e.g. 300-1000).
2. Configure `max_events_per_tick`, `event_workers`, and retry intervals for test runtime.

Assertions:

1. Processor drains queue over ticks without deadlock.
2. No unbounded goroutine growth.
3. Event throughput scales with `event_workers` until bounded by I/O.
4. DB lock errors do not appear persistently.

## Hash Integrity Assertion (Required)

For healed files, assert reconstructed hash is equal to action metadata hash:

1. Decode `CascadeMetadata` for the action (`DataHash` is base64-encoded hash payload).
2. Validate reconstructed file/key hash using same verification utility used in cascade flow (`cascadekit.VerifyB64DataHash` path).
3. Also assert observer-reported `reconstructed_hash_hex` matches recipient reconstructed local content.

## DB/State Assertions To Query

Primary tables:

- `self_healing_challenge_events`
- `self_healing_execution_metrics`

Key columns to assert:

- `challenge_id`
- `status` (`pending`, `processing`, `retry`, `completed`, `terminal`)
- `attempt_count`
- `lease_owner`, `lease_expires_at`
- `next_retry_at`
- `last_error`

## Execution Commands

1. `make setup-supernodes`
2. `make test-cascade` (baseline)
3. `go test ./tests/system -run TestSelfHealing -v`
4. `make test-e2e`

## Acceptance Criteria

1. All new self-healing E2E tests pass reliably on repeated runs.
2. No flaky dependence on local timing race; use polling with bounded timeouts.
3. Happy path proves:
   - weighted trigger,
   - reconstruction via reseed,
   - observer quorum,
   - hash integrity.
4. Failure-path tests prove:
   - retry policy,
   - terminal behavior,
   - restart-safe reclaim,
   - stale-window rejection.
