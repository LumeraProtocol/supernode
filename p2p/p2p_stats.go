package p2p

import (
	"context"
	"time"

	"sync/atomic"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	ristretto "github.com/dgraph-io/ristretto/v2"
)

const (
	// Cache layout:
	// - p2pStatsLKGKey is the long-lived last-known-good snapshot. Status requests serve from this
	//   snapshot immediately (low latency) and refresh it asynchronously when stale.
	// - p2pStatsFreshKey is a short-lived freshness marker. Its presence means “recently refreshed”,
	//   so we do not start another heavy refresh yet.
	p2pStatsLKGKey   = "p2p_stats/snapshot"
	p2pStatsFreshKey = "p2p_stats/fresh"

	// Knobs (tune latency vs freshness):
	//
	// p2pStatsFreshTTL:
	//   Minimum time between starting heavy diagnostic refreshes (DB stats, disk usage, peer list).
	//   On an incoming Stats() call (e.g. /api/v1/status?include_p2p_metrics=true), if the freshness
	//   marker is missing/expired, we trigger ONE background refresh and immediately return the
	//   current snapshot.
	//
	// p2pStatsCacheKeepAlive:
	//   How long we keep the last-known-good snapshot around for fallback when refreshes fail.
	//
	// p2pStatsRefreshTimeout:
	//   Per-refresh time budget for the heavy refresh goroutine.
	p2pStatsFreshTTL       = 30 * time.Second
	p2pStatsCacheKeepAlive = 10 * time.Minute
	p2pStatsRefreshTimeout = 6 * time.Second

	p2pStatsSlowRefreshThreshold = 750 * time.Millisecond
)

type p2pStatsManager struct {
	cache *ristretto.Cache[string, any]

	refreshInFlight atomic.Bool
}

func newP2PStatsManager() *p2pStatsManager {
	c, _ := ristretto.NewCache(&ristretto.Config[string, any]{
		NumCounters: 100,
		MaxCost:     10,
		BufferItems: 64,
	})
	return &p2pStatsManager{cache: c}
}

func (m *p2pStatsManager) getSnapshot() *StatsSnapshot {
	if m == nil || m.cache == nil {
		return nil
	}
	v, ok := m.cache.Get(p2pStatsLKGKey)
	if !ok {
		return nil
	}
	snap, _ := v.(*StatsSnapshot)
	return snap
}

func (m *p2pStatsManager) setSnapshot(snap *StatsSnapshot) {
	if m == nil || m.cache == nil || snap == nil {
		return
	}
	m.cache.SetWithTTL(p2pStatsLKGKey, snap, 1, p2pStatsCacheKeepAlive)
}

func (m *p2pStatsManager) isFresh() bool {
	if m == nil || m.cache == nil {
		return false
	}
	_, ok := m.cache.Get(p2pStatsFreshKey)
	return ok
}

func (m *p2pStatsManager) markFresh() {
	if m == nil || m.cache == nil {
		return
	}
	m.cache.SetWithTTL(p2pStatsFreshKey, true, 1, p2pStatsFreshTTL)
}

// Stats returns a typed snapshot compatible with the p2p.Client interface.
//
// Incoming call semantics:
//   - The call stays latency-predictable: it returns immediately from the cached snapshot.
//   - PeersCount is always refreshed via a fast DHT path on every call (no peer list allocation).
//   - Heavy diagnostics are refreshed in the background at most once per p2pStatsFreshTTL, deduped
//     across concurrent callers (refreshInFlight).
func (m *p2pStatsManager) Stats(ctx context.Context, p *p2p) (*StatsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	peersCount := int32(0)
	if p != nil && p.dht != nil {
		peersCount = int32(p.dht.PeersCount())
	}

	prev := m.getSnapshot()
	snap := cloneSnapshot(prev)
	snap.PeersCount = peersCount
	m.setSnapshot(cloneSnapshot(snap))

	if !m.isFresh() {
		m.maybeRefreshDiagnostics(ctx, p)
	}

	return snap, nil
}

func (m *p2pStatsManager) maybeRefreshDiagnostics(ctx context.Context, p *p2p) {
	if m == nil || p == nil {
		return
	}
	if !m.refreshInFlight.CompareAndSwap(false, true) {
		return
	}

	logCtx := ctx
	go func() {
		defer m.refreshInFlight.Store(false)

		start := time.Now()
		refreshCtx, cancel := context.WithTimeout(context.Background(), p2pStatsRefreshTimeout)
		err := m.refreshDiagnostics(refreshCtx, p)
		cancel()
		dur := time.Since(start)

		if err != nil {
			logtrace.Warn(logCtx, "p2p stats diagnostics refresh failed", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"refresh":            "diagnostics",
				"ms":                 dur.Milliseconds(),
				logtrace.FieldError:  err.Error(),
			})
		}
		if dur > p2pStatsSlowRefreshThreshold {
			logtrace.Warn(logCtx, "p2p stats diagnostics refresh slow", logtrace.Fields{
				logtrace.FieldModule: "p2p",
				"refresh":            "diagnostics",
				"ms":                 dur.Milliseconds(),
			})
		}
	}()
}

func (m *p2pStatsManager) refreshDiagnostics(ctx context.Context, p *p2p) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	prev := m.getSnapshot()
	next := cloneSnapshot(prev)

	var refreshErr error

	if p != nil && p.dht != nil {
		peers := p.dht.PeersSnapshot()
		next.Peers = peers
		next.PeersCount = int32(len(peers))
		next.NetworkHandleMetrics = p.dht.NetworkHandleMetricsSnapshot()
		dbStats, err := p.dht.DatabaseStats(ctx)
		if err != nil {
			refreshErr = err
		} else {
			next.Database = dbStats
		}
		next.BanList = p.dht.BanListSnapshot()
		next.ConnPool = p.dht.ConnPoolSnapshot()

		metricsSnap := p.dht.MetricsSnapshot()
		next.DHTMetrics = metricsSnap
	}

	if p != nil && p.config != nil {
		diskUse, err := utils.DiskUsage(p.config.DataDir)
		if err != nil {
			if refreshErr == nil {
				refreshErr = err
			}
		} else {
			next.DiskInfo = &diskUse
		}
	}

	m.setSnapshot(next)
	m.markFresh()
	return refreshErr
}

func cloneSnapshot(in *StatsSnapshot) *StatsSnapshot {
	if in == nil {
		return &StatsSnapshot{
			BanList:  []kademlia.BanSnapshot{},
			ConnPool: map[string]int64{},
		}
	}

	out := *in

	if in.Peers != nil {
		out.Peers = make([]*kademlia.Node, len(in.Peers))
		for i, peer := range in.Peers {
			if peer == nil {
				continue
			}
			cp := *peer
			if peer.ID != nil {
				cp.ID = append([]byte(nil), peer.ID...)
			}
			if peer.HashedID != nil {
				cp.HashedID = append([]byte(nil), peer.HashedID...)
			}
			out.Peers[i] = &cp
		}
	}

	if in.BanList != nil {
		out.BanList = append([]kademlia.BanSnapshot(nil), in.BanList...)
	} else {
		out.BanList = []kademlia.BanSnapshot{}
	}

	if in.ConnPool != nil {
		out.ConnPool = make(map[string]int64, len(in.ConnPool))
		for k, v := range in.ConnPool {
			out.ConnPool[k] = v
		}
	} else {
		out.ConnPool = map[string]int64{}
	}

	if in.NetworkHandleMetrics != nil {
		out.NetworkHandleMetrics = make(map[string]kademlia.HandleCounters, len(in.NetworkHandleMetrics))
		for k, v := range in.NetworkHandleMetrics {
			out.NetworkHandleMetrics[k] = v
		}
	}

	if in.DiskInfo != nil {
		du := *in.DiskInfo
		out.DiskInfo = &du
	}

	return &out
}
