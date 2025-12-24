package reachability

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var defaultStore atomic.Value // *Store

// SetDefaultStore sets the process-wide default reachability store.
// Components that don't have explicit dependency injection can use DefaultStore().
func SetDefaultStore(store *Store) {
	defaultStore.Store(store)
}

// DefaultStore returns the process-wide default reachability store, if set.
func DefaultStore() *Store {
	if v := defaultStore.Load(); v != nil {
		if s, ok := v.(*Store); ok {
			return s
		}
	}
	return nil
}

// Store records last-seen inbound evidence per Service.
// This is used to infer external reachability based on real inbound traffic.
type Store struct {
	mu sync.RWMutex

	lastInbound map[Service]time.Time
}

func NewStore() *Store {
	return &Store{
		lastInbound: make(map[Service]time.Time, 3),
	}
}

// RecordInbound stores an inbound evidence timestamp for a service.
//
// The caller should pass the remote identity when available (e.g. via ALTS auth info),
// and the remote network address.
//
// Note: reachability evidence intentionally does not validate/filter IPs (no IPv4/IPv6
// distinction, no public/private checks). Any successfully observed inbound traffic
// counts as evidence.
func (s *Store) RecordInbound(service Service, remoteIdentity string, remoteAddr net.Addr, at time.Time) {
	if service == "" {
		return
	}
	// We only treat this as inbound evidence if the caller can provide
	// an actual remote network address.
	if remoteAddr == nil {
		return
	}
	// remoteIdentity is currently not used for inference; it is accepted so we can
	// later add richer observability/reporting without changing call sites.
	_ = remoteIdentity

	s.mu.Lock()
	s.lastInbound[service] = at
	s.mu.Unlock()

	// Epoch bucketing is best-effort and only starts once the process has a known epoch.
	// This avoids logging/reporting "success in epoch 0" during early startup.
	if EpochInitialized() {
		if t := DefaultEpochTracker(); t != nil {
			t.Mark(CurrentEpochID(), service)
		}
	}
}

func (s *Store) LastInbound(service Service) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastInbound[service]
}

func (s *Store) IsInboundFresh(service Service, window time.Duration, now time.Time) bool {
	if window <= 0 {
		return false
	}
	last := s.LastInbound(service)
	if last.IsZero() {
		return false
	}
	return now.Sub(last) <= window
}
