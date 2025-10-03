# P2P Metrics — Current Behavior

We removed the custom per‑RPC metrics capture and the `pkg/p2pmetrics` package. Logs are the source of truth for store/retrieve visibility, and the Status API provides a rolling DHT snapshot for high‑level metrics.

What remains
- Status API metrics: DHT rolling windows (store success, batch retrieve), network handle counters, ban list, DB/disk stats, and connection pool metrics.
- Logs: detailed send/ok/fail lines for RPCs at both client and server.

What was removed
- Per‑RPC metrics capture and grouping by IP for events.
- Metrics collectors and context tagging helpers.
- Recent per‑request lists from the Status API.

Events
- The supernode emits minimal events (e.g., artefacts stored, downloaded). These events no longer include metrics payloads. Use logs for detailed troubleshooting.

Status API
- To include P2P metrics and peer info, clients set `include_p2p_metrics=true` on `StatusRequest`.
- The SDK adapter already includes this flag by default to populate peer count for eligibility checks.

References
- Status proto: `proto/supernode/status.proto`
- Service proto: `proto/supernode/service.proto`
