package conn

import (
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

// NewOutCounter returns an outgoing counter initialized to the starting sequence
// number for the client/server side of a connection.
func NewOutCounter(s Side, overflowLen int) (c Counter) {
	c.overflowLen = overflowLen
	if s == ServerSide {
		// Server counters in ALTS record have the little-endian high bit
		// set.
		c.value[counterLen-1] = 0x80
	}
	return
}

// NewInCounter returns an incoming counter initialized to the starting sequence
// number for the client/server side of a connection. This is used in ALTS record
// to check that incoming counters are as expected, since ALTS record guarantees
// that messages are unwrapped in the same order that the peer wrapped them.
func NewInCounter(s Side, overflowLen int) (c Counter) {
	c.overflowLen = overflowLen
	if s == ClientSide {
		// Server counters in ALTS record have the little-endian high bit
		// set.
		c.value[counterLen-1] = 0x80
	}
	return
}

// CounterFromValue creates a new counter given an initial value.
func CounterFromValue(value []byte, overflowLen int) (c Counter) {
	c.overflowLen = overflowLen
	copy(c.value[:], value)
	return
}

// CounterSide returns the connection side (client/server) a sequence counter is
// associated with.
func CounterSide(c []byte) Side {
	if c[counterLen-1]&0x80 == 0x80 {
		return ServerSide
	}
	return ClientSide
}
