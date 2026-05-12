# Evaluation Scenarios
Generated: 2026-04-28T21:20:09Z
Project: supernode

## How to Use
1. Use the current checkout that produced `docs/gates-evals/S01-gate-report.md`.
2. Execute each scenario step-by-step.
3. Record results in the checklists.
4. Fill the feedback form at the bottom.

---

## Scenario 1: F01/F02 - Epoch Report Payload Readiness
**Goal:** Confirm a validator operator can trust the supernode to submit Everlight-compatible epoch report data.
**Preconditions:** Go toolchain is installed; no local Lumera chain is required for the targeted behavior checks.
**Linked:** F01, F02, AT01, AT02, UF01

### Steps:
1. Run `go test ./supernode/host_reporter` -> Expected: tests pass, including cascade Kademlia DB byte measurement and prober/non-prober tick behavior.
2. Inspect `supernode/host_reporter/service.go` around the host report assembly -> Expected: `CascadeKademliaDbBytes` is set from local `data*.sqlite3*` files when present.
3. Inspect `supernode/host_reporter/tick_behavior_test.go` -> Expected: prober epochs submit observations for assigned targets; non-prober epochs submit none.
4. Run `go test ./pkg/lumera/modules/supernode` -> Expected: package compiles against Lumera query types for PoolState, SNEligibility, and PayoutHistory.

### Checklist:
- [x] Host reporter tests pass.
- [x] Cascade Kademlia DB bytes are reported in bytes, not MB.
- [x] Prober and non-prober observation behavior is clear.
- [x] Supernode query wrappers compile.

---

## Scenario 2: F03/F04 - STORAGE_FULL Node Behavior
**Goal:** Confirm STORAGE_FULL nodes remain usable for reads/routing while rejecting new writes.
**Preconditions:** Go toolchain is installed.
**Linked:** F03, F04, AT01, UF02

### Steps:
1. Run `go test ./p2p/kademlia` -> Expected: tests pass.
2. Inspect `p2p/kademlia/supernode_state_test.go` -> Expected: ACTIVE is routing+store eligible; POSTPONED and STORAGE_FULL are routing eligible but not store eligible.
3. Inspect `p2p/kademlia/supernode_state.go` -> Expected: comments and helpers describe routing/read eligibility separately from store/write eligibility.
4. Run `go test ./supernode/verifier` -> Expected: verifier tests pass for STORAGE_FULL allowance.

### Checklist:
- [x] STORAGE_FULL is not treated as eligible for new-key writes.
- [x] STORAGE_FULL remains routing/read eligible.
- [x] Existing-key replication/read preservation is clear from tests.
- [x] Verifier accepts STORAGE_FULL where intended.

---

## Scenario 3: F05 - Devnet Host Reporter Disable
**Goal:** Confirm a devnet operator can prevent account-sequence races when an external test driver submits epoch reports.
**Preconditions:** Go toolchain is installed; no live devnet is required for static/test evaluation.
**Linked:** F05, AT01, UF03

### Steps:
1. Run `go test ./supernode/cmd` -> Expected: command package tests pass.
2. Inspect `supernode/cmd/start.go` around `LUMERA_SUPERNODE_DISABLE_HOST_REPORTER` -> Expected: `1` or `true` disables host reporter startup only for the current process environment.
3. Confirm there is no config-file setting for this affordance -> Expected: production canonical path remains unchanged unless the env var is explicitly set.

### Checklist:
- [x] Command package tests pass.
- [x] Env var behavior is obvious to a devnet operator.
- [x] Production behavior is unchanged by default.

---

## Scenario 4: F06 - LEP-5 Availability Commitment Evidence
**Goal:** Confirm commitment/proof helpers remain executable and aligned with SDK evidence needs.
**Preconditions:** Go toolchain is installed.
**Linked:** F06, AT01

### Steps:
1. Run `go test ./pkg/cascadekit` -> Expected: commitment helper tests pass.
2. Inspect `pkg/cascadekit/commitment_test.go` -> Expected: tests cover valid roots and invalid root/chunk-size rejection.
3. Inspect `sdk/task/evidence.go` -> Expected: SDK evidence path still references the commitment/proof shape required by LEP-5.

### Checklist:
- [x] Cascadekit tests pass.
- [x] Valid commitment root behavior is covered.
- [x] Invalid commitment inputs are rejected.
- [x] SDK evidence path remains aligned with LEP-5 expectations.

---

## Scenario 5: Cross-Feature Retrospective Gate Smoke
**Goal:** Confirm the same critical Everlight checks used by the retrospective gate still pass from one command.
**Preconditions:** Go toolchain is installed.
**Linked:** F01, F02, F03, F04, F05, F06, S01

### Steps:
1. Run `go test -tags=e2e ./tests/e2e` -> Expected: E2E smoke test passes by executing targeted Everlight, LEP-5, and start-command package tests.
2. Open `docs/gates-evals/S01-gate-report.md` -> Expected: report says `OVERALL: PASS` and records no blocking Everlight functional issues.
3. Review warnings in the gate report -> Expected: WebP system header gap, D02 logging, and D03 diagnostics are warnings/follow-ups, not evidence that completed Everlight functionality is failing.

### Checklist:
- [x] E2E smoke command passes.
- [x] Gate report remains PASS.
- [x] Warnings are correctly understood as follow-up or environment items.

---

## Feedback Form

### Overall Assessment
- [ ] Ready for launch  - [ ] Minor fixes  - [ ] Major fixes

### Ratings (1-5): Usability ___ | Performance ___ | Polish ___

### Issues Found
| # | Severity | Feature | Description | Steps to Reproduce |
|---|----------|---------|-------------|-------------------|
| 1 |  |  |  |  |

### Suggestions

[Free form]
