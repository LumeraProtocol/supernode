# Changelog


## Upcoming Release: v2.4.9

This release focuses on stability, performance, and operational clarity across the cascade pipeline and SuperNode operation:

- **Action Lock** to enforce single-writer semantics during registration, preventing conflicting writes under load.
- Switched to the **original ZSTD C wrapper**, improving compression/decompression performance and reliability for large cascade uploads/downloads.
- Significant **logging cleanup** to reduce noise and surface only high-signal operational logs.
- General improvements to **registration and download reliability**, with better error surfaces and more predictable DHT behavior.

(Will be finalized on release.)
## Recent Releases
### v2.4.8 — 2025-11-19

- Fixed **layout signature format** to match the WASM-RQ implementation, keeping on-disk and verification formats aligned.

### v2.4.7 – v2.4.5 — 2025-11-18

- Fully aligned **Go SDK cascade signatures** with the JS/Keplr **ADR-36** format to avoid cross-client signature mismatches.

### v2.4.4 — 2025-11-17

- Removed `LocalCosmosAddress` from SDK config and now **derive the address from the keyring**, reducing configuration errors for operators.

### v2.4.3 — 2025-11-16

- Fixed SDK **SuperNode filter parameters** so selection and queries respect the intended filters.
- Added **explicit SuperNode rejection reasons**, so operators can see why a node was rejected.
- Added **ADR-36 signature support** and **dual-mode index verification** to keep Go SDK, JS SDK and on-chain verification in sync.

### v2.4.2 – v2.4.0 — 2025-11-05 to 2025-11-07

- Optimized **DHT symbol retrieval** with:
  - primary-provider waves,
  - streamed writes, and
  - bounded concurrency  
  → faster and more reliable downloads, especially for large cascades.
- Increased **Lumera connection timeout** and improved verifier error messages to behave better on slower or flaky networks.
- Updated **action price handling** (using string / safer types) to avoid precision and encoding issues.
- Upgraded to **Lumera v1.8.0** and **Go 1.25.1**, refreshed dependencies, and tightened configuration verification.
- Cleaned up **DHT summary logs** so download and symbol activity is easier to inspect.

### v2.3.93 – v2.3.92 — 2025-10-24

- Fixed **GitHub CI** issues after retagging Lumera and ensured releases build cleanly.
- Optimized **blake3 hashing** to reduce CPU overhead on large files.
- Updated **top SuperNode query** to accept all request parameters, enabling better ranking and filtering.

### v2.3.91 – v2.3.89 — 2025-10-22 to 2025-10-23

- Introduced **xor-based SuperNode ranking** combining XOR distance, RAM and storage to pick better nodes for registration and retrieval.
- Merged the **supernode refactor + Action SDK updates**, consolidating P2P logic and cleaning up services.
- Enhanced **SuperNode discovery** to find and rank good candidates more reliably.
- Relaxed the **hard equality check on action fee**, making fee validation robust to minor differences while keeping protection in place.

### v2.3.87 – v2.3.82 — 2025-10-10 to 2025-10-22

- Added **task tracking** and **deterministic SuperNode selection** in the SDK, improving observability and predictability of where tasks go.
- Added **version gating** in DHT and SDK, enforcing minimum versions so old or misconfigured nodes don’t break cascades.
- Ranked SuperNodes by **free RAM** and other capacity metrics, improving registration and download reliability under load.
- Optimized **symbol fetch** and cascade flows to reduce latency and unnecessary network calls.
- Added **cache for SuperNode info calls** and stricter **version checks** in the SDK to avoid repeated discovery overhead.
- Tightened **stable release checks** so nodes don’t silently run unsupported or experimental builds.

**Net effect of v2.3.8x – v2.4.x:**  
Drastically improved **cascade registration and download performance**, better **node selection and gating**, and a much cleaner **operator experience** (logs, errors, metrics, and signatures).


## Earlier Highlights

### Cascade Registration & Download

- End-to-end **cascade registration pipeline** that:
  - Validates metadata and pricing against Lumera.
  - Selects SuperNodes based on rank, version, and capacity.
  - Publishes tasks and tracks them until completion.
- **Download pipeline** backed by Kademlia DHT:
  - Multi-provider symbol retrieval with retries and backoff.
  - Concurrency controls to avoid overloading slow nodes.
  - Progressive streaming to disk to handle large files efficiently.
- Solid baseline for **performance and reliability** of both registration and retrieval flows.

### sn-manager (SuperNode Lifecycle & Auto-Upgrade)

- CLI tool to **install, configure, and manage SuperNodes**:
  - `init`, `start`, `stop`, `status`, `get`, `ls`, `ls-remote`, `check`, `use`.
- **GitHub release integration**:
  - Discover available versions.
  - Download and switch between them with minimal friction.
- **Zero-downtime upgrade path**:
  - Download new binary, health-check, and perform controlled switchover.
- Central place to manage **configuration, versions and auto-updates**, so operators don’t have to script everything themselves.

### SuperNode SDKs & sncli

- **SuperNode SDKs** (primarily Go, with JS alignment):
  - Deterministic **SuperNode selection** (version-aware, capacity-aware, rank-aware).
  - Built-in **version checks** so clients avoid incompatible nodes.
  - Consistent **ADR-36 signatures** across Go and JS, compatible with Keplr and Lumera.
- **sncli**:
  - gRPC reflection support for **dynamic service/method introspection**.
  - Secure gRPC connections using the **Lumera keyring** and TLS.
  - Configurable via flags and environment variables for easy scripting and automation.
- Together, these form the main integration surface for **apps, scripts, and operators** that need to talk to SuperNodes programmatically.

### Status API & Metrics

- **Enhanced Status API** with:
  - Version and build information.
  - CPU, memory, disk usage and percentages.
  - Network statistics, active connections and peer info.
  - Cascade service performance metrics and task enumeration.
  - System uptime and resource utilization tracking.
  - SuperNode ranking-related fields.
- Basis for **monitoring dashboards, health checks and alerting**.

### Infrastructure, Context & CI/CD

- **Context handling and server optimization**:
  - Detached contexts for SDK background operations.
  - Tuned gRPC server settings for large file handling and throughput.
  - Better error handling and context propagation across components.
- **Configuration refactoring**:
  - Unified network interface configuration across SuperNode, SDK and tooling.
- **CI/CD workflow optimization**:
  - Simplified GitHub Actions workflows.
  - Faster, more reliable builds for SuperNode and sn-manager.
  - Cleaner job organization and dependency management.

### Logging & P2P Reliability

- **Logging improvements**:
  - Disabled stack traces by default in production to reduce noise.
  - High-signal logs for DHT, cascade tasks and critical paths.
- **P2P reliability fixes**:
  - Corrected connection deadlines in Kademlia DHT to prevent timeouts.
  - Fixed race conditions in credentials handling and metrics writers.
  - Additional metrics and download instrumentation for better visibility.

