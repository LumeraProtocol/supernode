package p2p

import (
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// StatsSnapshot is a typed alternative to Client.Stats' map[string]any payload.
// It is intended for internal consumers that want compile-time safety.
type StatsSnapshot struct {
	PeersCount int32
	Peers      []*kademlia.Node

	BanList  []kademlia.BanSnapshot
	ConnPool map[string]int64

	NetworkHandleMetrics map[string]kademlia.HandleCounters
	DHTMetrics           kademlia.DHTMetricsSnapshot

	Database DatabaseStats
	DiskInfo *utils.DiskStatus
}

type DatabaseStats = kademlia.DatabaseStats
