package conn

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"reflect"
	"testing"

	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/grpctest"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	// Disable goroutine leak check — safe in CI context
	grpctest.RunSubTests(t, s{})
}

func init() {
	RegisterALTSRecordProtocols()
}

// testConn mimics a net.Conn to the peer.
type testConn struct {
	net.Conn
	in  *bytes.Buffer
	out *bytes.Buffer
}

func (c *testConn) Read(b []byte) (n int, err error) {
	return c.in.Read(b)
}

func (c *testConn) Write(b []byte) (n int, err error) {
	return c.out.Write(b)
}

func (c *testConn) Close() error {
	return nil
}

// getTestKeyForProtocol returns a key for the given record protocol.
func getTestKeyForProtocol(rp string) ([]byte, error) {
	var key []byte
	switch rp {
	case RecordProtocolAESGCM:
		key = []byte{
			// 16 arbitrary bytes.
			0x1f, 0x8b, 0x08, 0x00, 0x00, 0x09, 0x6e, 0x88, 0x02, 0xff, 0xe2, 0xd2, 0x4c, 0xce, 0x4f, 0x49}
	case RecordProtocolAESGCMReKey:
		key = []byte{
			// 44 arbitrary bytes: the first 32 bytes are used as a key for HKDF-expand
			// and the remaining 12 bytes are used as a random mask for the counter
			0xbc, 0x00, 0x27, 0x04, 0x52, 0x3a, 0xd5, 0x6f, 0x1a, 0xf0, 0xde, 0x5f, 0x63, 0x99, 0xd0, 0x19,
			0x50, 0xda, 0x34, 0x93, 0xc0, 0xdb, 0xe4, 0xca, 0xa0, 0x48, 0x68, 0x02, 0x15, 0x52, 0x58, 0xa4,
			0x12, 0x6c, 0x18, 0xee, 0xaa, 0xf6, 0xcd, 0x17, 0x81, 0xf8, 0xe2, 0x7f}
	case RecordProtocolXChaCha20Poly1305ReKey:
		key = []byte{
			// 56 bytes, the first 32 bytes are used as a secret key
			// and the remaining 24 bytes are used as public nonce
			0x36, 0xdb, 0xf3, 0x50, 0xab, 0x70, 0xb2, 0xdb, 0xe6, 0x92, 0x95, 0xde, 0xe1, 0x39, 0x0a, 0x90,
			0xad, 0x78, 0xe6, 0x00, 0x1a, 0xb8, 0x36, 0x16, 0x13, 0xc2, 0xc5, 0x91, 0xf1, 0x40, 0x25, 0x2f,
			0xdb, 0xb6, 0x3d, 0xc0, 0xd3, 0x45, 0x01, 0x0e, 0x12, 0xd9, 0xe3, 0x11, 0x0b, 0xc8, 0xbd, 0x2a,
			0x66, 0xa5, 0x83, 0xbd, 0xed, 0x6d, 0x13, 0xb4}
	default:
		return nil, fmt.Errorf("Unknown ALTS record protocol %q", rp)
	}
	return key, nil
}

func newTestALTSRecordConn(in, out *bytes.Buffer, side Side, rp string, protected []byte) *Conn {
	key, err := getTestKeyForProtocol(rp)
	if err != nil {
		panic(fmt.Sprintf("Unexpected error getting test key for protocol %q: %v", rp, err))
	}

	tc := testConn{
		in:  in,
		out: out,
	}
	c, err := NewConn(&tc, side, rp, key, protected)
	if err != nil {
		panic(fmt.Sprintf("Unexpected error creating test ALTS record connection: %v", err))
	}
	return c.(*Conn)
}

func newConnPair(rp string, clientProtected []byte, serverProtected []byte) (client, server *Conn) {
	clientBuf := new(bytes.Buffer)
	serverBuf := new(bytes.Buffer)
	clientConn := newTestALTSRecordConn(clientBuf, serverBuf, ClientSide, rp, clientProtected)
	serverConn := newTestALTSRecordConn(serverBuf, clientBuf, ServerSide, rp, serverProtected)
	return clientConn, serverConn
}

func testPingPong(t *testing.T, rp string) {
	clientConn, serverConn := newConnPair(rp, nil, nil)
	clientMsg := []byte("Client Message")
	if n, err := clientConn.Write(clientMsg); n != len(clientMsg) || err != nil {
		t.Fatalf("Client Write() = %v, %v; want %v, <nil>", n, err, len(clientMsg))
	}
	rcvClientMsg := make([]byte, len(clientMsg))
	if n, err := serverConn.Read(rcvClientMsg); n != len(rcvClientMsg) || err != nil {
		t.Fatalf("Server Read() = %v, %v; want %v, <nil>", n, err, len(rcvClientMsg))
	}
	if !reflect.DeepEqual(clientMsg, rcvClientMsg) {
		t.Fatalf("Client Write()/Server Read() = %v, want %v", rcvClientMsg, clientMsg)
	}

	serverMsg := []byte("Server Message")
	if n, err := serverConn.Write(serverMsg); n != len(serverMsg) || err != nil {
		t.Fatalf("Server Write() = %v, %v; want %v, <nil>", n, err, len(serverMsg))
	}
	rcvServerMsg := make([]byte, len(serverMsg))
	if n, err := clientConn.Read(rcvServerMsg); n != len(rcvServerMsg) || err != nil {
		t.Fatalf("Client Read() = %v, %v; want %v, <nil>", n, err, len(rcvServerMsg))
	}
	if !reflect.DeepEqual(serverMsg, rcvServerMsg) {
		t.Fatalf("Server Write()/Client Read() = %v, want %v", rcvServerMsg, serverMsg)
	}
}

func (s) TestPingPong(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testPingPong(t, rp)
	}
}

func testSmallReadBuffer(t *testing.T, rp string) {
	clientConn, serverConn := newConnPair(rp, nil, nil)
	msg := []byte("Very Important Message")
	if n, err := clientConn.Write(msg); err != nil {
		t.Fatalf("Write() = %v, %v; want %v, <nil>", n, err, len(msg))
	}
	rcvMsg := make([]byte, len(msg))
	n := 2 // Arbitrary index to break rcvMsg in two.
	rcvMsg1 := rcvMsg[:n]
	rcvMsg2 := rcvMsg[n:]
	if n, err := serverConn.Read(rcvMsg1); n != len(rcvMsg1) || err != nil {
		t.Fatalf("Read() = %v, %v; want %v, <nil>", n, err, len(rcvMsg1))
	}
	if n, err := serverConn.Read(rcvMsg2); n != len(rcvMsg2) || err != nil {
		t.Fatalf("Read() = %v, %v; want %v, <nil>", n, err, len(rcvMsg2))
	}
	if !reflect.DeepEqual(msg, rcvMsg) {
		t.Fatalf("Write()/Read() = %v, want %v", rcvMsg, msg)
	}
}

func (s) TestSmallReadBuffer(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testSmallReadBuffer(t, rp)
	}
}

func testLargeMsg(t *testing.T, rp string) {
	clientConn, serverConn := newConnPair(rp, nil, nil)
	// msgLen is such that the length in the framing is larger than the
	// default size of one frame.
	msgLen := altsRecordDefaultLength - msgTypeFieldSize - clientConn.crypto.EncryptionOverhead() + 1
	msg := make([]byte, msgLen)
	if n, err := clientConn.Write(msg); n != len(msg) || err != nil {
		t.Fatalf("Write() = %v, %v; want %v, <nil>", n, err, len(msg))
	}
	rcvMsg := make([]byte, len(msg))
	if n, err := io.ReadFull(serverConn, rcvMsg); n != len(rcvMsg) || err != nil {
		t.Fatalf("Read() = %v, %v; want %v, <nil>", n, err, len(rcvMsg))
	}
	if !reflect.DeepEqual(msg, rcvMsg) {
		t.Fatalf("Write()/Server Read() = %v, want %v", rcvMsg, msg)
	}
}

func (s) TestLargeMsg(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testLargeMsg(t, rp)
	}
}

func testIncorrectMsgType(t *testing.T, rp string) {
	// framedMsg is an empty ciphertext with correct framing but wrong
	// message type.
	framedMsg := make([]byte, MsgLenFieldSize+msgTypeFieldSize)
	binary.LittleEndian.PutUint32(framedMsg[:MsgLenFieldSize], msgTypeFieldSize)
	wrongMsgType := uint32(0x22)
	binary.LittleEndian.PutUint32(framedMsg[MsgLenFieldSize:], wrongMsgType)

	in := bytes.NewBuffer(framedMsg)
	c := newTestALTSRecordConn(in, nil, ClientSide, rp, nil)
	b := make([]byte, 1)
	if n, err := c.Read(b); n != 0 || err == nil {
		t.Fatalf("Read() = <nil>, want %v", fmt.Errorf("received frame with incorrect message type %v", wrongMsgType))
	}
}

func (s) TestIncorrectMsgType(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testIncorrectMsgType(t, rp)
	}
}

func testFrameTooLarge(t *testing.T, rp string) {
	buf := new(bytes.Buffer)
	clientConn := newTestALTSRecordConn(nil, buf, ClientSide, rp, nil)
	serverConn := newTestALTSRecordConn(buf, nil, ServerSide, rp, nil)
	// payloadLen is such that the length in the framing is larger than
	// allowed in one frame.
	payloadLen := altsRecordLengthLimit - msgTypeFieldSize - clientConn.crypto.EncryptionOverhead() + 1
	payload := make([]byte, payloadLen)
	c, err := clientConn.crypto.Encrypt(nil, payload)
	if err != nil {
		t.Fatalf("Error encrypting message: %v", err)
	}
	msgLen := msgTypeFieldSize + len(c)
	framedMsg := make([]byte, MsgLenFieldSize+msgLen)
	binary.LittleEndian.PutUint32(framedMsg[:MsgLenFieldSize], uint32(msgTypeFieldSize+len(c)))
	msg := framedMsg[MsgLenFieldSize:]
	binary.LittleEndian.PutUint32(msg[:msgTypeFieldSize], altsRecordMsgType)
	copy(msg[msgTypeFieldSize:], c)
	if _, err = buf.Write(framedMsg); err != nil {
		t.Fatalf("Unexpected error writing to buffer: %v", err)
	}
	b := make([]byte, 1)
	if n, err := serverConn.Read(b); n != 0 || err == nil {
		t.Fatalf("Read() = <nil>, want %v", fmt.Errorf("received the frame length %d larger than the limit %d", altsRecordLengthLimit+1, altsRecordLengthLimit))
	}
}

func (s) TestFrameTooLarge(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testFrameTooLarge(t, rp)
	}
}

func testWriteLargeData(t *testing.T, rp string) {
	// Test sending and receiving messages larger than the maximum write
	// buffer size.
	clientConn, serverConn := newConnPair(rp, nil, nil)
	// Message size is intentionally chosen to not be multiple of
	// payloadLengthLimit.
	msgSize := altsWriteBufferMaxSize + (100 * 1024)
	clientMsg := make([]byte, msgSize)
	for i := 0; i < msgSize; i++ {
		clientMsg[i] = 0xAA
	}
	if n, err := clientConn.Write(clientMsg); n != len(clientMsg) || err != nil {
		t.Fatalf("Client Write() = %v, %v; want %v, <nil>", n, err, len(clientMsg))
	}
	// We need to keep reading until the entire message is received. The
	// reason we set all bytes of the message to a value other than zero is
	// to avoid ambiguous zero-init value of rcvClientMsg buffer and the
	// actual received data.
	rcvClientMsg := make([]byte, 0, msgSize)
	numberOfExpectedFrames := int(math.Ceil(float64(msgSize) / float64(serverConn.payloadLengthLimit)))
	for i := 0; i < numberOfExpectedFrames; i++ {
		expectedRcvSize := serverConn.payloadLengthLimit
		if i == numberOfExpectedFrames-1 {
			// Last frame might be smaller.
			expectedRcvSize = msgSize % serverConn.payloadLengthLimit
		}
		tmpBuf := make([]byte, expectedRcvSize)
		if n, err := serverConn.Read(tmpBuf); n != len(tmpBuf) || err != nil {
			t.Fatalf("Server Read() = %v, %v; want %v, <nil>", n, err, len(tmpBuf))
		}
		rcvClientMsg = append(rcvClientMsg, tmpBuf...)
	}
	if !reflect.DeepEqual(clientMsg, rcvClientMsg) {
		t.Fatalf("Client Write()/Server Read() = %v, want %v", rcvClientMsg, clientMsg)
	}
}

func (s) TestWriteLargeData(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testWriteLargeData(t, rp)
	}
}

func testProtectedBuffer(t *testing.T, rp string) {
	key, err := getTestKeyForProtocol(rp)
	if err != nil {
		t.Fatalf("Unexpected error getting test key for protocol %q: %v", rp, err)
	}

	// Encrypt a message to be passed to NewConn as a client-side protected
	// buffer.
	newCrypto := protocols[rp]
	if newCrypto == nil {
		t.Fatalf("Unknown record protocol %q", rp)
	}
	crypto, err := newCrypto(ClientSide, key)
	if err != nil {
		t.Fatalf("Failed to create a crypter for protocol %q: %v", rp, err)
	}
	msg := []byte("Client Protected Message")
	encryptedMsg, err := crypto.Encrypt(nil, msg)
	if err != nil {
		t.Fatalf("Failed to encrypt the client protected message: %v", err)
	}
	protectedMsg := make([]byte, 8)                                          // 8 bytes = 4 length + 4 type
	binary.LittleEndian.PutUint32(protectedMsg, uint32(len(encryptedMsg))+4) // 4 bytes for the type
	binary.LittleEndian.PutUint32(protectedMsg[4:], altsRecordMsgType)
	protectedMsg = append(protectedMsg, encryptedMsg...)

	_, serverConn := newConnPair(rp, nil, protectedMsg)
	rcvClientMsg := make([]byte, len(msg))
	if n, err := serverConn.Read(rcvClientMsg); n != len(rcvClientMsg) || err != nil {
		t.Fatalf("Server Read() = %v, %v; want %v, <nil>", n, err, len(rcvClientMsg))
	}
	if !reflect.DeepEqual(msg, rcvClientMsg) {
		t.Fatalf("Client protected/Server Read() = %v, want %v", rcvClientMsg, msg)
	}
}

func (s) TestProtectedBuffer(t *testing.T) {
	for _, rp := range ALTSRecordProtocols {
		testProtectedBuffer(t, rp)
	}
}
