package kademlia

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"google.golang.org/grpc/credentials"
)

const defaultCapacity = 256

type connectionItem struct {
	lastAccess time.Time
	conn       net.Conn
}

// ConnPool is a manager of connection pool
type ConnPool struct {
	capacity int
	conns    map[string]*connectionItem
	mtx      sync.Mutex
	metrics  *PoolMetrics
}

// NewConnPool return a connection pool
func NewConnPool(ctx context.Context) *ConnPool {
	m := &PoolMetrics{}
	pool := &ConnPool{
		capacity: defaultCapacity,
		conns:    map[string]*connectionItem{},
		metrics:  m,
	}
	m.Capacity.Store(int64(pool.capacity))

	return pool
}

// connWrapper implements wrapper of secure connection
type connWrapper struct {
	secureConn net.Conn
	rawConn    net.Conn
	mtx        sync.Mutex
}

// NewSecureClientConn does client handshake and returns a secure, pooled-ready connection.
func NewSecureClientConn(ctx context.Context, tc credentials.TransportCredentials, remoteAddr string) (net.Conn, error) {
	// Extract identity if in Lumera format (e.g., "<bech32>@ip:port")
	remoteIdentity, remoteAddress, err := ltc.ExtractIdentity(remoteAddr, true)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	base, ok := tc.(*ltc.LumeraTC)
	if !ok {
		return nil, fmt.Errorf("invalid credentials type")
	}

	// Per-connection clone; set remote identity on the clone only.
	cloned, ok := base.Clone().(*ltc.LumeraTC)
	if !ok {
		return nil, fmt.Errorf("failed to clone LumeraTC")
	}
	cloned.SetRemoteIdentity(remoteIdentity)

	// Dial the remote address with a short timeout.
	d := net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	rawConn, err := d.DialContext(ctx, "tcp", remoteAddress)
	if err != nil {
		return nil, errors.Errorf("dial %q: %w", remoteAddress, err)
	}

	// Disable Nagle for lower-latency small RPCs.
	if tcp, ok := rawConn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	// Clear any global deadline; per-RPC deadlines are set in Network.Call.
	_ = rawConn.SetDeadline(time.Time{})

	// TLS/ALTS-ish client handshake using the per-connection cloned creds.
	secureConn, _, err := cloned.ClientHandshake(ctx, "", rawConn)
	if err != nil {
		_ = rawConn.Close()
		return nil, errors.Errorf("client secure establish %q: %w", remoteAddress, err)
	}

	return &connWrapper{
		secureConn: secureConn,
		rawConn:    rawConn,
	}, nil
}

// NewSecureServerConn do server handshake and create a secure connection
func NewSecureServerConn(_ context.Context, tc credentials.TransportCredentials, rawConn net.Conn) (net.Conn, error) {
	if tcp, ok := rawConn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(2 * time.Minute) // tune: 2–5 min
	}

	if tcp, ok := rawConn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	conn, _, err := tc.ServerHandshake(rawConn)
	if err != nil {
		return nil, errors.Errorf("server secure establish failed: %w", err)
	}

	return &connWrapper{
		secureConn: conn,
		rawConn:    rawConn,
	}, nil
}

// Read implements net.Conn's Read interface
func (conn *connWrapper) Read(b []byte) (n int, err error) {
	if conn == nil || conn.secureConn == nil {
		return 0, io.ErrClosedPipe
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.secureConn.Read(b)
}

// Write implements net.Conn's Write interface
func (conn *connWrapper) Write(b []byte) (n int, err error) {
	if conn == nil || conn.secureConn == nil {
		return 0, io.ErrClosedPipe
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.secureConn.Write(b)
}

// Close implements net.Conn's Close interface
func (conn *connWrapper) Close() error {
	if conn == nil {
		return nil
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	if conn.secureConn != nil {
		_ = conn.secureConn.Close()
	}
	if conn.rawConn != nil {
		return conn.rawConn.Close()
	}
	return nil
}

// LocalAddr implements net.Conn's LocalAddr interface
func (conn *connWrapper) LocalAddr() net.Addr {
	if conn == nil || conn.rawConn == nil {
		return nil
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.rawConn.LocalAddr()
}

// RemoteAddr implements net.Conn's RemoteAddr interface
func (conn *connWrapper) RemoteAddr() net.Addr {
	if conn == nil || conn.rawConn == nil {
		return nil
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.rawConn.RemoteAddr()
}

// SetDeadline implements net.Conn's SetDeadline interface
func (conn *connWrapper) SetDeadline(t time.Time) error {
	if conn == nil || conn.secureConn == nil {
		return io.ErrClosedPipe
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.secureConn.SetDeadline(t)
}

// SetReadDeadline implements net.Conn's SetReadDeadline interface
func (conn *connWrapper) SetReadDeadline(t time.Time) error {
	if conn == nil || conn.secureConn == nil {
		return io.ErrClosedPipe
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.secureConn.SetReadDeadline(t)
}

// SetWriteDeadline implements net.Conn's SetWriteDeadline interface
func (conn *connWrapper) SetWriteDeadline(t time.Time) error {
	if conn == nil || conn.secureConn == nil {
		return io.ErrClosedPipe
	}
	conn.mtx.Lock()
	defer conn.mtx.Unlock()
	return conn.secureConn.SetWriteDeadline(t)
}

// optional: expose a getter
func (pool *ConnPool) MetricsSnapshot() map[string]int64 {
	if pool.metrics == nil {
		return map[string]int64{}
	}
	return pool.metrics.Snapshot()
}

// optional: allow changing capacity (updates gauge)
func (pool *ConnPool) SetCapacity(n int) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	pool.capacity = n
	if pool.metrics != nil {
		pool.metrics.Capacity.Store(int64(n))
	}
}

func (pool *ConnPool) Add(addr string, conn net.Conn) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()

	if w, ok := conn.(*connWrapper); ok && w == nil {
		return
	}

	if item, ok := pool.conns[addr]; ok {
		_ = item.conn.Close()
		pool.conns[addr] = &connectionItem{lastAccess: time.Now().UTC(), conn: conn}
		if pool.metrics != nil {
			pool.metrics.Replacements.Add(1)
			// OpenCurrent unchanged (we closed one, added one)
		}
		return
	}

	// capacity-based eviction
	if len(pool.conns) >= pool.capacity {
		oldestAccess := time.Now().UTC()
		oldestKey := ""
		for k, it := range pool.conns {
			if it.lastAccess.Before(oldestAccess) {
				oldestAccess = it.lastAccess
				oldestKey = k
			}
		}
		if oldestKey != "" {
			if it, ok := pool.conns[oldestKey]; ok {
				_ = it.conn.Close()
			}
			delete(pool.conns, oldestKey)
			if pool.metrics != nil {
				pool.metrics.EvictionsLRU.Add(1)
			}
			// OpenCurrent will be incremented when we add below (net +/- 0)
		}
	}

	pool.conns[addr] = &connectionItem{lastAccess: time.Now().UTC(), conn: conn}
	if pool.metrics != nil {
		pool.metrics.Adds.Add(1)
		pool.metrics.OpenCurrent.Store(int64(len(pool.conns)))
	}
}

func (pool *ConnPool) Get(addr string) (net.Conn, error) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	item, ok := pool.conns[addr]
	if !ok {
		if pool.metrics != nil {
			pool.metrics.Misses.Add(1)
		}
		return nil, fmt.Errorf("not found")
	}
	item.lastAccess = time.Now().UTC()
	if pool.metrics != nil {
		pool.metrics.ReuseHits.Add(1)
	}
	return item.conn, nil
}

func (pool *ConnPool) Del(addr string) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	if it, ok := pool.conns[addr]; ok {
		_ = it.conn.Close()
		delete(pool.conns, addr)
		if pool.metrics != nil {
			pool.metrics.OpenCurrent.Store(int64(len(pool.conns)))
		}
	}
}

func (pool *ConnPool) Release() {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	for addr, item := range pool.conns {
		_ = item.conn.Close()
		delete(pool.conns, addr)
	}
	if pool.metrics != nil {
		pool.metrics.OpenCurrent.Store(0)
	}
}

type PoolMetrics struct {
	// counters
	Adds         atomic.Int64 // new unique Add (not replacement)
	Replacements atomic.Int64 // Add replacing an existing entry
	ReuseHits    atomic.Int64 // Get hit
	Misses       atomic.Int64 // Get miss
	EvictionsLRU atomic.Int64 // capacity-based evictions
	PrunedIdle   atomic.Int64 // idle evictions via PruneIdle

	// gauge-ish
	OpenCurrent atomic.Int64 // current open conns tracked by pool
	Capacity    atomic.Int64 // configured capacity
}

func (m *PoolMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"adds_total":          m.Adds.Load(),
		"replacements_total":  m.Replacements.Load(),
		"reuse_hits_total":    m.ReuseHits.Load(),
		"misses_total":        m.Misses.Load(),
		"evictions_lru_total": m.EvictionsLRU.Load(),
		"pruned_idle_total":   m.PrunedIdle.Load(),
		"open_current":        m.OpenCurrent.Load(),
		"capacity":            m.Capacity.Load(),
	}
}

// PruneIdle closes conns idle for >= idleFor and removes them from the pool.
func (pool *ConnPool) PruneIdle(idleFor time.Duration) {
	pool.mtx.Lock()
	defer pool.mtx.Unlock()
	now := time.Now().UTC()
	pruned := int64(0)
	for addr, it := range pool.conns {
		if now.Sub(it.lastAccess) >= idleFor {
			_ = it.conn.Close()
			delete(pool.conns, addr)
			pruned++
		}
	}
	if pool.metrics != nil {
		if pruned > 0 {
			pool.metrics.PrunedIdle.Add(pruned)
			pool.metrics.OpenCurrent.Store(int64(len(pool.conns)))
		}
	}
}

// StartPruner runs PruneIdle every interval; call from Network.NewNetwork or similar.
func (pool *ConnPool) StartPruner(ctx context.Context, interval, idleFor time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pool.PruneIdle(idleFor)
			}
		}
	}()
}
