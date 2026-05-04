# LEP-6 Supernode Release Runbook

This runbook covers the Supernode-side LEP-6 storage-truth enforcement support introduced across the LEP-6 PR stack and finalized in PR-6.

## Scope

Supernode LEP-6 provides runtime support for Lumera `v1.12.0` audit/storage-truth APIs:

- storage challenge ticket discovery and transcript/evidence submission;
- storage recheck candidate discovery, local retry budget, and `MsgSubmitStorageRecheckEvidence` submission;
- self-healing heal-op dispatch, healer claim submission, verifier attestation submission, and finalizer publication only after chain-verified heal success;
- repo-native in-process observability snapshots plus structured `logtrace` events.

The chain remains the source of truth for heal-op scheduling, verifier assignment, verification quorum, rejected/failed/expired status, and scoring/probation changes.

## Release prerequisites

1. Supernode must depend on Lumera `v1.12.0` APIs.
2. Operators must run against a Lumera chain whose audit module includes LEP-6 storage-truth endpoints.
3. Supernode local SQLite storage must be writable; PR-6 adds local idempotency state for pending/submitted heal and recheck txs.
4. Existing Supernode status/log collection should be enabled so LEP-6 snapshot counters and structured logs are visible through the same operator workflow used by storage challenge, Cascade, and supernode metrics.

## Local validation commands

From the supernode repository root:

```bash
export PATH=/home/openclaw/.local/go/bin:$PATH
go test $(go list ./... | grep -v '/tests')
```

For the real-chain LEP-6 system test:

```bash
make system-test-setup
make test-lep6
```

`make test-lep6` runs `tests/system/TestLEP6RealChainIntegration` using the same real `lumerad`/local-chain harness as Cascade e2e. It does not use chain mocks.

## Observability

LEP-6 uses the repo-native Supernode observability pattern: in-process atomic snapshots plus structured `logtrace` fields. PR-6 does **not** add a LEP-6-only Prometheus endpoint.

LEP-6 snapshot signals include:

- challenge dispatch results by chain result class;
- challenge dispatch throttling drops by reason;
- challenge dispatch epoch duration totals/counts by role;
- ticket discovery outcomes;
- no-ticket-provider-active state;
- recheck candidates discovered and current pending candidate gauge;
- recheck submissions by result class/result;
- recheck already-submitted dedupe count;
- recheck failure counts by stage;
- heal claims by result;
- heal claim reconciliation count;
- heal verifications by result/vote;
- heal verification already-recorded dedupe count;
- self-healing pending claim gauge;
- self-healing staging bytes gauge;
- finalizer publish count;
- finalizer cleanup count by terminal chain status.

Suggested alerts/signals from snapshots/logs:

- sustained heal-claim `submit_error` or `stage_error` increases;
- sustained heal-verification `submit_error` or `stage_error` increases;
- sustained recheck failure increases by stage;
- challenge dispatch throttling drops approaching the chain cap;
- no-ticket-provider-active remaining true after candidate-producing epochs;
- self-healing staging bytes increasing without matching finalizer publish/cleanup progress;
- rejected/failed/expired finalizer cleanup spikes after a release.

## Operational behavior

### Successful healing

1. Chain schedules a heal-op and assigns a healer/verifiers.
2. Healer stages recovered data locally and pre-stages a local dedup row.
3. Healer submits `MsgClaimHealComplete`.
4. On chain acceptance, Supernode marks the local row as submitted.
5. Verifiers fetch and verify the staged manifest/hash, pre-stage local dedup rows, and submit `MsgSubmitHealVerification`.
6. Once chain marks the heal-op verified, the finalizer publishes the healed artifact to the P2P layer.

Important: the healed file is not published as durable P2P recovery output before successful chain verification.

### Rejected healing

If verifier quorum rejects the heal, the chain marks the heal-op rejected/failed according to Lumera `v1.12.0` keeper rules. Supernode does not publish the healer output as recovered data.

### Healer cannot heal / no-show

If the healer cannot produce a valid manifest or misses the deadline, the chain eventually expires/fails the heal-op and applies LEP-6 scoring/probation rules. Supernode records errors and retry/backoff state locally where applicable, but does not override chain status.

### Restart/idempotency

PR-6 closes the submit-success/persist-crash window by pre-staging local pending rows before chain tx submission for:

- heal claims;
- heal verifications;
- recheck evidence submissions.

Pending rows dedup retries after restart; successful txs are marked submitted after chain acceptance. Submit failures remove the pending row so the operation can retry later.

## Troubleshooting

- If duplicate tx errors appear after restart, inspect local SQLite `status` values for LEP-6 pending/submitted tables and compare with chain heal/recheck state.
- If recheck candidates stop processing, inspect `recheck_attempt_failures`; failures expire after the configured TTL and successful submissions clear the failure budget.
- If LEP-6 counters are flat while work is expected, inspect service startup/configuration first, then check structured `logtrace` events for the challenge, recheck, and self-healing services.
- If `make test-lep6` fails before tests start, run `make system-test-setup` and confirm `lumerad version` matches the Lumera dependency version.
