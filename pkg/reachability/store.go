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

	selfIdentity string

	lastInbound map[Service]time.Time
}

func NewStore(selfIdentity string) *Store {
	return &Store{
		selfIdentity: selfIdentity,
		lastInbound:  make(map[Service]time.Time, 3),
	}
}

// RecordInbound stores an inbound evidence timestamp for a service.
//
// The caller should pass the remote identity when available (e.g. via ALTS auth info),
// and the remote address (for loopback filtering).
func (s *Store) RecordInbound(service Service, remoteIdentity string, remoteAddr net.Addr, at time.Time) {
	if service == "" {
		return
	}
	// We only treat this as "external inbound evidence" if the caller can
	// provide an actual remote network address.
	if remoteAddr == nil {
		return
	}
	// The system is IPv4-only; ignore IPv6 / non-IP addresses.
	if !isIPv4Addr(remoteAddr) {
		return
	}
	if isLoopbackAddr(remoteAddr) {
		return
	}
	if remoteIdentity != "" && s.selfIdentity != "" && remoteIdentity == s.selfIdentity {
		return
	}

	s.mu.Lock()
	s.lastInbound[service] = at
	s.mu.Unlock()
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

func isLoopbackAddr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP != nil && a.IP.IsLoopback()
	case *net.UDPAddr:
		return a.IP != nil && a.IP.IsLoopback()
	default:
		// Best-effort fallback; if we can't reliably parse, don't exclude.
		return false
	}
}

func isIPv4Addr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP != nil && a.IP.To4() != nil
	case *net.UDPAddr:
		return a.IP != nil && a.IP.To4() != nil
	default:
		return false
	}
}
