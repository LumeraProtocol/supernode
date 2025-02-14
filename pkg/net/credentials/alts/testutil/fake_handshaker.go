package testutil

import (
	"bytes"
	"fmt"
	"time"
	"net"

	"github.com/cosmos/gogoproto/proto"
	lumeraidtypes "github.com/LumeraProtocol/lumera/x/lumeraid/types"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

// FakeHandshakeConn implements a fake handshake connection
type FakeHandshakeConn struct {
	net.Conn
	in           *bytes.Buffer
	out          *bytes.Buffer
	readLatency  time.Duration
	remoteAddr   string
	localAddr    string
	handshakeErr error // for simulating handshake errors
}

func NewFakeHandshakeConn(localAddr, remoteAddr string) *FakeHandshakeConn {
	return &FakeHandshakeConn{
		in:         new(bytes.Buffer),
		out:        new(bytes.Buffer),
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
	}
}

func (c *FakeHandshakeConn) Read(b []byte) (n int, err error) {
	time.Sleep(c.readLatency)
	if c.handshakeErr != nil {
		return 0, c.handshakeErr
	}
	return c.in.Read(b)
}

func (c *FakeHandshakeConn) Write(b []byte) (n int, err error) {
	if c.handshakeErr != nil {
		return 0, c.handshakeErr
	}
	return c.out.Write(b)
}

func (c *FakeHandshakeConn) Close() error {
	return nil
}

// FakeHandshaker simulates a peer in the handshake process
type FakeHandshaker struct {
	conn       *FakeHandshakeConn
	signature  []byte
	pubKey     []byte
	peerType   int32
	curve      string
}

func NewFakeHandshaker(conn *FakeHandshakeConn) *FakeHandshaker {
	return &FakeHandshaker{
		conn: conn,
	}
}

func (h *FakeHandshaker) SimulateHandshake(isClient bool) error {
	// Read incoming handshake
	handshakeBytes, _, err := ReceiveHandshakeMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to read handshake: %w", err)
	}

	// Parse handshake info
	var info lumeraidtypes.HandshakeInfo
	if err := proto.Unmarshal(handshakeBytes, &info); err != nil {
		return fmt.Errorf("failed to unmarshal handshake info: %w", err)
	}

	// Create response
	responseInfo := &lumeraidtypes.HandshakeInfo{
		Address:   h.conn.localAddr,
		PeerType:  h.peerType,
		PublicKey: h.pubKey,
		Curve:     h.curve,
	}

	responseBytes, err := proto.Marshal(responseInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Send response
	if err := SendHandshakeMessage(h.conn, responseBytes, h.signature); err != nil {
		return fmt.Errorf("failed to send response: %w", err)
	}

	return nil
}

func (h *FakeHandshaker) SimulateError(err error) {
	h.conn.handshakeErr = err
}