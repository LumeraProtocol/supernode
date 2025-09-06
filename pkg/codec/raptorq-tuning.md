RaptorQ Tuning (<= 1 GB Artefacts)


No environment setup required
- We do not set or rely on environment variables in our deployments. Defaults below apply automatically.

Quick defaults (used automatically)
- Symbol size: 65535 bytes
- Redundancy: 5
- Max memory cap: 8 GB
- Concurrency: min(NumCPU, 8)

Why these defaults
- 1 GB inputs decode comfortably with a 6–8 GB cap including headroom for symbol buffers and hashing.
- Concurrency scales with CPU but is capped at 8 to avoid oversubscription on large hosts.

Operational notes (plain‑English)
- Progressive retrieval: Start small (~9% of symbols), try to rebuild, then step up (25%, 50%, 75%, 100%) only if needed. Saves time, bandwidth, and memory.
- Decode hygiene: Symbols are saved to disk and removed from RAM before decoding, lowering peak memory use.
- Clear failure behavior: Integrity issues trigger “fetch more and retry”; path/I/O issues stop immediately with a clear error.
- Temporary files: Decoder returns both the final file path and the temp directory; delete the temp directory after use to free space.
- No full pre‑fetch: The system is designed to succeed with fewer symbols, so you don’t need to collect everything first.

Progressive decode explained (simple examples)
- What it means: We try to rebuild the file with a small portion of pieces first. If that’s enough, we stop. If not, we fetch a bit more and try again.
- Why it helps: Many times, you don’t need every piece to rebuild the file. Stopping early saves time and memory.

Example A (finishes early)
- Total symbols available: 10,000 (this is just how the file was encoded).
- Step 1: Fetch ~9% ≈ 900 symbols → Try to rebuild → Not enough.
- Step 2: Fetch up to 25% ≈ 2,500 symbols → Try → Success.
- Outcome: We stop here. No need to fetch the remaining 7,500 symbols.

Example B (needs more to repair)
- Total symbols: 10,000.
- Step 1: 9% (900) → Try → Not enough.
- Step 2: 25% (2,500) → Try → Still not enough (some peers are missing pieces).
- Step 3: 50% (5,000) → Try → Success.
- Outcome: We fetched half, rebuilt successfully, and avoided downloading 100%.

What you need to do
- Nothing. This is automatic. You don’t pick a percentage — the system decides and stops as soon as it has enough.

Memory controls at a glance (optional)
- GOMEMLIMIT: Go’s soft memory limit for the Go heap.
  - Scope: Go runtime only (not native C/Rust memory).
  - Default: unset. Example: `GOMEMLIMIT=7GiB` keeps Go heap around 7 GB.
- GOGC: Go GC aggressiveness (percent growth before next GC).
  - Scope: Go runtime only. Default: 100. Example: `GOGC=75` for lower peaks (more GC).
- LUMERA_RQ_MAX_MEMORY_MB: Native decoder memory cap (MB).
  - Scope: native RaptorQ. Default: 8192 (8 GB).
- LUMERA_RQ_CONCURRENCY: Native decoder parallelism limit.
  - Scope: native RaptorQ. Default: min(NumCPU, 8).
- LUMERA_RQ_SYMBOL_SIZE: Symbol size (bytes).
  - Default: 65535. Change only if you know why.
- LUMERA_RQ_REDUNDANCY: Repair factor.
  - Default: 5. Higher = more resilience and storage.

Suggested profiles (only if you choose to use envs)
- Constrained (8 GB RAM, 4 cores): `GOMEMLIMIT=4GiB`, `GOGC=75`, `LUMERA_RQ_MAX_MEMORY_MB=4096`, `LUMERA_RQ_CONCURRENCY=2`.
- Mid (16–32 GB RAM, 8 cores): `GOMEMLIMIT=7GiB`, `GOGC=75`, `LUMERA_RQ_MAX_MEMORY_MB=8192`, `LUMERA_RQ_CONCURRENCY=4`.
- Large (≥64 GB RAM, ≥16 cores): `GOMEMLIMIT=12GiB`, `GOGC=100`, `LUMERA_RQ_MAX_MEMORY_MB=12288`, `LUMERA_RQ_CONCURRENCY=8`.

Extra tips (no code changes)
- Avoid running several 1 GB restores at the same time on one host.
- Keep symbols/temp on fast storage (SSD/NVMe) with free space.
- Avoid very large/verbose logs during restore to reduce heap churn.
- Prefer hosts without other memory‑heavy jobs during large restores.
