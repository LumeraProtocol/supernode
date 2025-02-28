// Package testutil include useful test utilities for the handshaker.
package testutil

import (
	"encoding/binary"
	"net"
	"sync"
	"time"

	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

// Stats is used to collect statistics about concurrent handshake calls.
type Stats struct {
	mu                 sync.Mutex
	calls              int
	MaxConcurrentCalls int
}

// Update updates the statistics by adding one call.
func (s *Stats) Update() func() {
	s.mu.Lock()
	s.calls++
	if s.calls > s.MaxConcurrentCalls {
		s.MaxConcurrentCalls = s.calls
	}
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		s.calls--
		s.mu.Unlock()
	}
}

// Reset resets the statistics.
func (s *Stats) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = 0
	s.MaxConcurrentCalls = 0
}

// testConn mimics a net.Conn to the peer.
type testLatencyConn struct {
	net.Conn
	readLatency time.Duration
}

// NewTestConnWithReadLatency wraps a net.Conn with artificial read latency
func NewTestConnWithReadLatency(conn net.Conn, readLatency time.Duration) net.Conn {
	return &testLatencyConn{
		Conn:       conn,
		readLatency: readLatency,
	}
}

// Read reads from the in buffer.
func (c *testLatencyConn) Read(b []byte) (n int, err error) {
	time.Sleep(c.readLatency)
	return c.Conn.Read(b)
}

// Write writes to the out buffer.
func (c *testLatencyConn) Write(b []byte) (n int, err error) {
	return c.Conn.Write(b)
}

// Close closes the testConn object.
func (c *testLatencyConn) Close() error {
	return nil
}

// unresponsiveTestConn mimics a net.Conn for an unresponsive peer. It is used
// for testing the PeerNotResponding case.
type unresponsiveTestConn struct {
	net.Conn
	delay time.Duration
}

// NewUnresponsiveTestConn creates a new instance of unresponsiveTestConn object.
func NewUnresponsiveTestConn(delay time.Duration) net.Conn {
	return &unresponsiveTestConn{
		delay: delay,
	}
}

// Read reads from the in buffer.
func (c *unresponsiveTestConn) Read([]byte) (n int, err error) {
    // Wait for delay to simulate network latency
    time.Sleep(c.delay)
    // Return empty data (success but zero bytes)
	return 0, nil
}

// Write writes to the out buffer.
func (c *unresponsiveTestConn) Write([]byte) (n int, err error) {
	return 0, nil
}

// Close closes the TestConn object.
func (c *unresponsiveTestConn) Close() error {
	return nil
}

// MakeFrame creates a handshake frame
func MakeFrame(payload []byte) []byte {
	frame := make([]byte, MsgLenFieldSize+len(payload)) // length field + payload
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[MsgLenFieldSize:], payload)
	return frame
}
