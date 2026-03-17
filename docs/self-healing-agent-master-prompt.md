# Master Prompt: Implement Self-Healing E2E Coverage

Use this prompt as-is with an implementation agent.

---

You are working in:

- Repo: `/Users/bilaltanveer/GolangProjects/src/Lumera Protocol/supernode`
- Branch: `jawad/self-healing-phase2-supernode`

## Objective

Implement production-grade system E2E coverage for self-healing, aligned with current off-chain self-healing architecture.

Use this plan as authoritative guidance:

- `docs/self-healing-e2e-test-plan.md`

## Required Context To Read First

1. `tests/system/README.md`
2. `tests/system/e2e_cascade_test.go`
3. `tests/system/supernode-utils.go`
4. `supernode/self_healing/service.go`
5. `supernode/transport/grpc/self_healing/handler.go`
6. `supernode/cascade/reseed.go`
7. `pkg/storage/queries/self_healing.go`
8. `pkg/storage/queries/sqlite.go`
9. `proto/supernode/self_healing.proto`

## Constraints

1. Do not introduce on-chain phase-4 changes.
2. Keep behavior aligned with weighted watchlist + action-driven targets.
3. Do not revert unrelated branch changes.
4. Keep tests deterministic with bounded polling (no fragile sleeps only).
5. Reuse existing system harness patterns and conventions.

## Deliverables

1. Add self-healing E2E tests in `tests/system`:
   - `tests/system/e2e_self_healing_test.go`
   - `tests/system/self_healing_helpers.go`
2. Cover scenarios:
   - happy path (reseed + verify + complete),
   - recipient down retry/terminal,
   - observer quorum failure,
   - duplicate replay once-per-window,
   - restart-safe lease reclaim,
   - stale-window terminal,
   - bounded scale smoke.
3. Add hash-integrity assertions:
   - reconstructed data must match action metadata hash.
4. If needed for correctness, make minimal harness hardening changes (for example per-node HOME isolation in system tests so sqlite is node-local).
5. Keep code formatted (`gofmt`) and maintain readable block formatting.

## Test Requirements

1. Weighted trigger must be exercised through multi-reporter epoch reports (`AuditMsg().SubmitEpochReport`) and not via local-only shortcuts.
2. Reconstruction path must validate that recipient healed via `RecoveryReseed` path semantics.
3. Assertions must include DB event status progression from `pending` to terminal/completed states where relevant.
4. Assertions must verify no duplicate processing for same deterministic challenge ID in the same window.

## Execution Commands

Run and report output for:

1. `make setup-supernodes`
2. `go test ./tests/system -run TestSelfHealing -v`
3. `make test-e2e`

If environment-specific linker/toolchain constraints block full run, still run all reachable subsets and explicitly report:

- what passed,
- what failed,
- exact blocker,
- why blocker is environmental vs logic.

## Quality Bar

Do not stop at happy path.

Implementation is complete only when:

1. all listed scenarios are implemented,
2. assertions are explicit and meaningful,
3. no obvious race/flaky timing patterns remain,
4. code is clean and maintainable.

## Final Output Format

Provide:

1. concise summary of changes,
2. file-by-file change list,
3. test command results,
4. known risks or follow-ups.

---
