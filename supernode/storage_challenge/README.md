# Storage Challenge (Supernode)

This package implements the Supernode side of the Storage Challenge protocol. The chain side lives in `lumera/x/audit/v1`
and provides deterministic epoch cadence (`epoch_id`) and a per-epoch `EpochAnchor` record (seed + frozen eligible sets).

## High-level flow

1. A background service runs on each Supernode (see `Service.Run`).
2. On each tick:
   - query latest chain height,
   - query `x/audit` params (and validate them),
   - derive `epoch_id` deterministically from `(height, epoch_zero_height, epoch_length_blocks)`,
   - fetch `EpochAnchor(epoch_id)` (retrying when the anchor is not committed yet at an epoch boundary),
   - deterministically decide if this node is a challenger for the epoch,
   - if challenger, select a bounded set of local file keys and run challenges.

## Deterministic selection

Selection must match the chain rules. The service uses:
- `EpochAnchor.Seed` and `epoch_id` for deterministic selection/jitter,
- `EpochAnchor.ActiveSupernodeAccounts` for challenger selection,
- sorted local file keys to ensure stable file-key selection.

## Challenge execution

For each selected `file_key`:
1. Select the replica set deterministically from the active set.
2. Pick a recipient and observers (excluding self).
3. Request a proof slice from the recipient via gRPC (`GetSliceProof`).
4. Validate the proof hash locally.
5. Ask observers to verify the proof (`VerifySliceProof`) and enforce quorum (if configured).

## Evidence submission

On failure conditions (recipient error/unreachable, invalid proof, observer quorum failure), and only when enabled:
- Build `StorageChallengeFailureEvidenceMetadata` JSON.
- Submit `MsgSubmitEvidence` to `x/audit` using the Supernode’s Cosmos key.

Evidence is chain-verifiable at the policy/assignment level (epoch anchor + deterministic rules), while full transcripts remain
off-chain (supernode logs and local stores).

## Notes / operational behavior

- Idempotency: the service tracks `lastRunEpoch` and runs at most once per epoch (per process) once it determines whether
  it is a challenger for that epoch.
- Anchor boundary: it is expected that `EpochAnchor(epoch_id)` may not be queryable immediately at an epoch boundary; the
  service retries on the next tick.
- Candidate key lookback: lookback is computed as `lookback_epochs * estimated_epoch_duration`, where epoch duration is
  estimated from recent blocks. If estimation fails, it falls back to `24h * lookback_epochs`.
