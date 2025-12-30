package supernode_metrics

import "time"

// =============================================================================
// Timing & Initialization
// =============================================================================

const (
	// DefaultStartupDelaySeconds is the grace period after process start before
	// metrics reporting and active probing begin. This allows the node to fully
	// initialize (establish P2P connections, sync state, etc.) before participating
	// in the reachability protocol.
	DefaultStartupDelaySeconds = 300

	// FallbackMetricsUpdateIntervalBlocks is used when chain params are unavailable
	// or return an invalid/zero value. This should be conservative and stable.
	FallbackMetricsUpdateIntervalBlocks = 100

	// ProbeTimeoutSeconds is the maximum time allowed for a single probe operation
	// (dial + handshake/request). Applies to gRPC health checks, HTTP status requests,
	// and P2P handshakes. Prevents slow or unresponsive peers from blocking the
	// probing loop.
	ProbeTimeoutSeconds = 5

	// EvidenceWindowSeconds defines how long inbound traffic evidence remains valid.
	// If a port received traffic within this window, it's considered OPEN.
	// Active probing is designed to refresh evidence before this window expires.
	//
	// Example: With 7200s (2 hours), a port that received traffic 30 minutes ago
	// is still OPEN; traffic 3 hours ago is stale and doesn't count.
	EvidenceWindowSeconds = 7200
)

// =============================================================================
// Active Probing Configuration
// =============================================================================
//
// Active probing generates deterministic peer-to-peer traffic so that
// quiet-but-healthy nodes can prove they're reachable. The system works as:
//
//  1. Each epoch, every ACTIVE node is assigned K target peers to probe
//  2. Conversely, each node expects to receive probes from ~K peers
//  3. If a node receives ANY inbound traffic → port is OPEN
//  4. If a node receives NO traffic, but enough assigned probers are alive
//     (quorum met) → port is CLOSED (silence is meaningful)
//  5. If quorum is NOT met → port is UNKNOWN (can't trust silence)
//
// =============================================================================

const (
	// ProbeAssignmentsPerEpoch (K) is the number of distinct peers assigned to
	// probe each target node per epoch.
	//
	// How it works:
	//   - Each ACTIVE sender probes K different receivers per epoch
	//   - Each receiver expects inbound probes from ~K different senders
	//   - Higher K = more redundancy but more network traffic
	//
	// Example with K=3:
	//   Node A is assigned to probe: [B, C, D]
	//   Node B is assigned to probe: [A, C, E]
	//   Node C expects probes from:  [A, B, ...] (whoever was assigned to C)
	//
	// Total probes per epoch = |ACTIVE nodes| × K
	ProbeAssignmentsPerEpoch = 3

	// ProbeQuorum is the minimum number of alive assigned probers required
	// before "no inbound traffic" can be interpreted as CLOSED.
	//
	// The quorum rule prevents false CLOSED verdicts when probers are offline:
	//   - If you expect K=3 probers but only 1 is alive, silence might just
	//     mean the 2 offline probers never tried to reach you
	//   - With Quorum=2, at least 2 of your assigned probers must be alive
	//     (recently reported metrics) before silence implies CLOSED
	//
	// Decision table (K=3, Quorum=2):
	//   ┌─────────────────┬───────────────────┬─────────────┐
	//   │ Alive Probers   │ Inbound Evidence  │ Port State  │
	//   ├─────────────────┼───────────────────┼─────────────┤
	//   │ 3 (all alive)   │ Yes               │ OPEN        │
	//   │ 3 (all alive)   │ No                │ CLOSED      │
	//   │ 2 (one dead)    │ Yes               │ OPEN        │
	//   │ 2 (one dead)    │ No                │ CLOSED      │
	//   │ 1 (two dead)    │ Yes               │ OPEN        │
	//   │ 1 (two dead)    │ No                │ UNKNOWN     │ ← quorum not met
	//   │ 0 (all dead)    │ No                │ UNKNOWN     │ ← quorum not met
	//   └─────────────────┴───────────────────┴─────────────┘
	//
	// Constraint: ProbeQuorum must be ≤ ProbeAssignmentsPerEpoch
	ProbeQuorum = 2

	// MinProbeAttemptsPerReportInterval ensures we complete at least this many
	// probe rounds per metrics reporting interval, even in small networks where
	// the deterministic schedule assigns fewer targets.
	//
	// This prevents the edge case where a node with only 1 assigned target would
	// probe too infrequently to keep evidence fresh within EvidenceWindowSeconds.
	//
	// The probe interval is calculated as:
	//   interval = reportInterval / max(assignedTargets, MinProbeAttemptsPerReportInterval)
	MinProbeAttemptsPerReportInterval = 3
)

// =============================================================================
// Default Ports
// =============================================================================

const (
	// Well-known service ports for supernode operation.
	// These defaults align with the chain's `required_open_ports` parameter.
	// Individual nodes may override via configuration.

	APIPort    = 4444 // gRPC API (supernode service, health checks)
	P2PPort    = 4445 // Kademlia DHT / P2P network
	StatusPort = 8002 // HTTP gateway (REST API, /api/v1/status)
)

// =============================================================================
// Internal Timing (unexported)
// =============================================================================

const (
	// defaultProbeInterval is the base interval between probe rounds when
	// chain-derived timing is unavailable.
	defaultProbeInterval = 2 * time.Minute

	// defaultProbeJitterFraction adds ±20% randomization to probe timing
	// to prevent thundering herd effects where all nodes probe simultaneously.
	defaultProbeJitterFraction = 0.20
)
