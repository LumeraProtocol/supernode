# Everlight Supernode Compatibility Plan (from Lumera PR #113)

Author: NightCrawler
Date: 2026-04-14
Status: Proposed implementation plan (supernode-side)

---

## 1) Scope and objective

This plan defines **all required supernode-side changes** so `supernode` is fully compatible with Lumera Everlight Phase 1 behavior introduced in `lumera` PR #113.

Primary goals:

1. Supernode emits the **right epoch report data** for Everlight payout and storage-full state transitions.
2. Supernode audit epoch-report submission remains valid under current audit-module validation semantics.
3. Supernode/query usage is aligned with chain changes around `STORAGE_FULL` and new payout query surfaces.
4. Supernode + SDK behavior is covered by deterministic tests (unit + integration/system-level).

---

## 2) Ground truth from chain (what changed and what supernode must respect)

From `lumera` PR #113 and latest follow-up commits:

- Everlight payout weighting uses `audit.v1 HostReport.cascade_kademlia_db_bytes`.
- `STORAGE_FULL` transition authority is now audit epoch report path (not legacy supernode metrics path).
- `SubmitEpochReport` semantics on chain currently enforce:
  - if reporter is prober in epoch: peer observations for assigned targets are required,
  - if reporter is not prober: peer observations are rejected.
- `GetTopSuperNodesForBlock` default selection excludes `POSTPONED` + `STORAGE_FULL` unless explicit state filters are used.
- New supernode query surfaces exist on chain:
  - `pool-state`, `sn-eligibility`, `payout-history`.

Implication: supernode must submit valid epoch reports (host + role-appropriate peer observations) and include cascade bytes in host report.

---

## 3) Current supernode behavior and gaps (RCA)

### 3.1 Current host reporter (`supernode/host_reporter/service.go`)

Current behavior:
- Submits one `MsgSubmitEpochReport` per epoch.
- Queries assigned targets and builds `StorageChallengeObservations` correctly.
- Reports `disk_usage_percent` from local storage metrics.

Gap:
- **Does not populate `HostReport.cascade_kademlia_db_bytes`**.

Impact:
- Everlight payout eligibility/weight can evaluate as missing/zero bytes, causing payout misses or exclusion despite real stored data.

### 3.2 Audit submit path (`pkg/lumera/modules/audit_msg/impl.go`)

Current behavior:
- Builds and submits `MsgSubmitEpochReport`.
- Defensive copy of observations.

Status:
- Compatible; no required API-level changes.

### 3.3 Supernode chain query usage

Current usage:
- Cascade selection path relies on `GetTopSupernodes` for action workflows.
- Bootstrap/routing path also uses list/top queries depending on component.

Risk area:
- Default top query excludes `STORAGE_FULL`. This is desired for storage-action selection but must be understood by features expecting compute-eligible storage-full behavior.

### 3.4 Legacy metrics collector (`supernode/supernode_metrics/*`)

Current status:
- Legacy metrics tx path is superseded by audit epoch reports for this feature.

Required posture:
- Do not rely on this path for Everlight payout bytes or storage-full transitions.

---

## 4) Required implementation changes (supernode repo)

## A) Mandatory functional changes

### A1. Populate `cascade_kademlia_db_bytes` in host epoch report

Target:
- `supernode/host_reporter/service.go`

Required behavior:
- During `tick()`, set:
  - `HostReport.CascadeKademliaDbBytes` = current node’s measured Cascade Kademlia DB bytes.

Accepted source options (implementation choice):
1. Reuse existing P2P SQLite size calculation path (`sqliteOnDiskSizeBytes`) via an exported accessor.
2. Reuse status subsystem database metrics if available as absolute bytes (preferred to avoid duplicate logic).

Important:
- Keep unit consistent with chain expectation: **bytes (not MB)**.
- If only MB is available from status pipeline, convert MB -> bytes deterministically.

### A2. Keep epoch report submission role-aware and valid

`host_reporter` already builds observations from `GetAssignedTargets`; preserve this.

Hard requirements:
- For prober epochs: include one observation per assigned target with required port count.
- For non-prober epochs: send no observations.

### A3. Operational logging clarity

Add structured log fields on successful submit:
- epoch_id
- disk_usage_percent
- cascade_kademlia_db_bytes
- observations_count
- assigned_targets_count

Purpose: rapid production diagnosis when payout/eligibility is questioned.

---

## B) Query/client integration additions (recommended)

### B1. Add supernode query client wrappers for Everlight surfaces

Target:
- `pkg/lumera/modules/supernode` (or equivalent query module)

Add methods:
- `GetPoolState(ctx)`
- `GetSNEligibility(ctx, validatorAddr)`
- `GetPayoutHistory(ctx, validatorAddr, pagination)`

Why:
- Needed for operator diagnostics and future automation.
- Enables supernode runtime health endpoints to expose payout-readiness status.

### B2. Add optional compatibility diagnostics endpoint/command

Expose a compact “Everlight readiness” check in supernode runtime/admin tooling:
- current epoch report bytes value
- current state (ACTIVE/STORAGE_FULL/etc)
- `sn-eligibility` result + reason

---

## C) Selection/behavior policy alignment checks

### C1. Cascade action selection assumptions

Review call sites that depend on `GetTopSupernodes` default filtering.

Goal:
- Ensure storage actions still intentionally avoid `STORAGE_FULL` nodes unless explicit policy says otherwise.
- Document this intentionally in code comments near selection points.

### C2. P2P bootstrap path sanity

Paths using supernode list/top queries for routing/bootstrap should be reviewed for unintended exclusion impacts.

Expected:
- bootstrap should not silently collapse due to state filter assumptions.

---

## 5) Tests required on supernode side

## Unit tests

### U1. Host report bytes population

File:
- `supernode/host_reporter/service_test.go`

Verify:
- `cascade_kademlia_db_bytes` is populated and >0 when DB exists.
- Units are bytes.
- fallback behavior when measurement unavailable.

### U2. Epoch role submission correctness

Verify:
- prober epoch -> observations count matches assigned targets.
- non-prober epoch -> observations omitted.
- no nil observations.

### U3. Report payload compatibility

Via mocked audit msg module:
- `SubmitEpochReport` payload includes disk + cascade bytes + expected observation structure.

## Integration tests (supernode repo)

### I1. End-to-end epoch report compatibility

With local chain/devnet harness:
- ensure report accepted for both prober/non-prober epochs.
- ensure no `invalid peer observations` under normal operation.

### I2. Everlight query smoke checks (if wrappers added)

Verify wrappers decode:
- `pool-state`, `sn-eligibility`, `payout-history`.

### I3. Storage-full/payout readiness diagnostics

After a high disk report:
- state transition visible via chain query.
- `sn-eligibility` reason/value observable.

---

## 6) SDK / shared client considerations

If `sdk-go` or shared internal clients are used by supernode admin tools:
- align with latest query/CLI flags and request shapes.
- ensure audit assigned-targets and epoch-anchor calls use current flag/proto semantics.

No protocol-breaking SDK changes are required for `SubmitEpochReport`; this is payload completeness + query wrapper work.

---

## 7) Rollout plan (safe order)

1. Implement host bytes population (`cascade_kademlia_db_bytes`) + unit tests.
2. Add/confirm role-aware observation tests.
3. Add query wrappers + smoke tests.
4. Run supernode integration test flow against lumera branch with Everlight.
5. Document runtime/operator diagnostics commands.
6. Release with explicit compatibility notes.

---

## 8) Risk register

- **R1**: Wrong units (MB vs bytes) -> payout distortion.
- **R2**: Missing observations for prober epochs -> rejected reports.
- **R3**: Query wrapper drift against chain proto updates.
- **R4**: Using default top-node query where STORAGE_FULL inclusion is expected.

Mitigations are covered by U1/U2/I1/I2/C1 checks.

---

## 9) Definition of done

Supernode-side compatibility is complete when:

- host reporter includes `cascade_kademlia_db_bytes` in submitted epoch reports,
- epoch reports are accepted in both prober/non-prober roles without manual intervention,
- query wrappers for Everlight surfaces are available and tested,
- integration run confirms no report-validation regressions,
- docs include operator guidance for readiness and troubleshooting.

---

## 10) PR breakdown recommendation (supernode)

- PR A: host reporter payload + tests (mandatory)
- PR B: query wrappers + diagnostics + tests (recommended)
- PR C: optional bootstrap/selection policy hardening docs/comments

This allows smallest-risk merge path while unblocking Everlight compatibility quickly.
