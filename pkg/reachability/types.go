package reachability

// Service identifies which externally-reachable service/port we are tracking.
// It is intentionally coarse-grained (per service), because `open_ports` is
// derived from the configured ports for each service.
type Service string

const (
	ServiceGRPC    Service = "grpc"
	ServiceP2P     Service = "p2p"
	ServiceGateway Service = "gateway"
)
