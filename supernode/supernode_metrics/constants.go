package supernode_metrics

import "time"

const (
	// DefaultStartupDelaySeconds is a safety delay after process start before
	// we begin reporting metrics / probing, giving the node time to fully initialize.
	DefaultStartupDelaySeconds = 30

	// ProbeTimeoutSeconds bounds how long we wait when actively probing peer
	// reachability, so a single slow dial cannot stall the entire loop.
	ProbeTimeoutSeconds = 5

	// MinProbeAttemptsPerReportInterval ensures we attempt at least this many outbound
	// probe rounds per metrics reporting interval, even when the deterministic schedule
	// would otherwise result in fewer targets (e.g. very small networks).
	//
	// A "probe attempt" here means probing one peer once (gRPC health, gateway status,
	// and P2P handshake are attempted as part of that single peer probe).
	MinProbeAttemptsPerReportInterval = 3

	// EvidenceWindowSeconds controls how long inbound traffic evidence is treated as fresh.
	// Active probing should ensure evidence is refreshed before it expires.
	EvidenceWindowSeconds = 600

	// ProbeAssignmentsPerEpoch is the number of distinct peers that should probe
	// each target per epoch (K in the spec).
	ProbeAssignmentsPerEpoch = 3
	// ProbeQuorum is the minimum number of alive assigned probers required to
	// infer CLOSED from lack of inbound evidence.
	ProbeQuorum = 2

	// Well-known local ports used when reporting `open_ports` metrics.
	// These are defaults; individual nodes may override them via config.
	// They should stay aligned with the chain's `required_open_ports` parameter.
	APIPort    = 4444 // Supernode gRPC port
	P2PPort    = 4445 // Kademlia / P2P port
	StatusPort = 8002 // HTTP gateway port (grpc-gateway: /api/v1/status)
)

const (
	defaultProbeInterval       = 2 * time.Minute
	defaultPeerRefreshInterval = 10 * time.Minute // only used when chain height is unavailable
	defaultProbeJitterFraction = 0.20
)
