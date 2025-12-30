package p2p

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	ristretto "github.com/dgraph-io/ristretto/v2"
	"golang.org/x/sync/singleflight"
	"sync/atomic"
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

type p2pStatsSnapshot struct {
	PeersCount int

	DHT        map[string]any
	BanList    []kademlia.BanSnapshot
	ConnPool   map[string]int64
	DHTMetrics kademlia.DHTMetricsSnapshot
	DiskInfo   *utils.DiskStatus
}

type p2pStatsManager struct {
	cache *ristretto.Cache[string, any]
	sf    singleflight.Group

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

func (m *p2pStatsManager) getSnapshot() *p2pStatsSnapshot {
	if m == nil || m.cache == nil {
		return nil
	}
	v, ok := m.cache.Get(p2pStatsLKGKey)
	if !ok {
		return nil
	}
	snap, _ := v.(*p2pStatsSnapshot)
	return snap
}

func (m *p2pStatsManager) setSnapshot(snap *p2pStatsSnapshot) {
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

// Stats returns a status map compatible with the existing p2p.Client.Stats API.
//
// Incoming call semantics:
//   - The call stays latency-predictable: it returns immediately from the cached snapshot.
//   - PeersCount is always refreshed via a fast DHT path on every call (no peer list allocation).
//   - Heavy diagnostics are refreshed in the background at most once per p2pStatsFreshTTL, deduped
//     across concurrent callers (singleflight + refreshInFlight).
//
// The status service only calls Stats() when include_p2p_metrics=true; when false, no refresh work
// is triggered at all.
func (m *p2pStatsManager) Stats(ctx context.Context, p *p2p) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	peersCount := 0
	if p != nil && p.dht != nil {
		peersCount = p.dht.PeersCount()
	}

	prev := m.getSnapshot()
	snap := &p2pStatsSnapshot{}
	if prev != nil {
		*snap = *prev
	}
	snap.PeersCount = peersCount
	m.setSnapshot(snap)

	if !m.isFresh() {
		m.maybeRefreshDiagnostics(ctx, p)
	}

	return snapshotToMap(snap, p), nil
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
		_, err, _ := m.sf.Do("p2p_stats/refresh_diagnostics", func() (any, error) {
			refreshCtx, cancel := context.WithTimeout(context.Background(), p2pStatsRefreshTimeout)
			defer cancel()
			return nil, m.refreshDiagnostics(refreshCtx, p)
		})
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
	next := &p2pStatsSnapshot{}
	if prev != nil {
		*next = *prev
	}

	var refreshErr error

	if p != nil && p.dht != nil {
		dhtStats, err := p.dht.Stats(ctx)
		if err != nil {
			refreshErr = err
		} else if dhtStats != nil {
			next.DHT = dhtStats
		}
		next.BanList = p.dht.BanListSnapshot()
		next.ConnPool = p.dht.ConnPoolSnapshot()

		metricsSnap := p.dht.MetricsSnapshot()
		next.DHTMetrics = metricsSnap
		if next.DHT != nil {
			next.DHT["dht_metrics"] = metricsSnap
		}
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

func snapshotToMap(snap *p2pStatsSnapshot, p *p2p) map[string]interface{} {
	ret := map[string]interface{}{}

	dhtStats := map[string]any{}
	if snap != nil && snap.DHT != nil {
		dhtStats = make(map[string]any, len(snap.DHT)+2)
		for k, v := range snap.DHT {
			dhtStats[k] = v
		}
	}
	if snap != nil {
		dhtStats["peers_count"] = snap.PeersCount
		ret["dht_metrics"] = snap.DHTMetrics
		dhtStats["dht_metrics"] = snap.DHTMetrics

		bans := snap.BanList
		if bans == nil {
			bans = []kademlia.BanSnapshot{}
		}
		pool := snap.ConnPool
		if pool == nil {
			pool = map[string]int64{}
		}
		ret["ban-list"] = bans
		ret["conn-pool"] = pool

		if snap.DiskInfo != nil {
			ret["disk-info"] = snap.DiskInfo
		}
	} else {
		ret["ban-list"] = []kademlia.BanSnapshot{}
		ret["conn-pool"] = map[string]int64{}
	}

	ret["dht"] = dhtStats
	if p != nil {
		ret["config"] = p.config
	}
	return ret
}
