package common

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	lumeraidtypes "github.com/LumeraProtocol/lumera/x/lumeraid/types"
)

// configurableConn is a net.Conn implementation that can be configured to fail on read and/or write.
type configurableConn struct {
	// If set to true, Read will return readErr instead of reading from readBuffer.
	failOnRead bool
	// If set to true, Write will return writeErr.
	failOnWrite bool

	// The error to return when a read failure is simulated.
	readErr error
	// The error to return when a write failure is simulated.
	writeErr error

	// When not failing, Read will pull data from this buffer.
	readBuffer *bytes.Buffer
}

// Read implements net.Conn's Read method.
func (c *configurableConn) Read(b []byte) (int, error) {
	if c.failOnRead {
		return 0, c.readErr
	}
	if c.readBuffer == nil {
		// No data to read.
		return 0, io.EOF
	}
	return c.readBuffer.Read(b)
}

// Write implements net.Conn's Write method.
func (c *configurableConn) Write(b []byte) (int, error) {
	if c.failOnWrite {
		return 0, c.writeErr
	}
	// For simplicity, if not failing, pretend the data was written successfully.
	return len(b), nil
}

func (c *configurableConn) Close() error {
	return nil
}

func (c *configurableConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (c *configurableConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

func (c *configurableConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *configurableConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *configurableConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// TestSendAndReceiveHandshakeMessage verifies that a handshake message sent over a connection
// is properly read on the other end.
func TestSendAndReceiveHandshakeMessage(t *testing.T) {
	// Create an in-memory full-duplex network connection.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	handshakeBytes := []byte("handshake-data")
	signature := []byte("signature-data")

	// Start a goroutine to send the handshake message.
	go func() {
		if err := SendHandshakeMessage(clientConn, handshakeBytes, signature); err != nil {
			t.Errorf("SendHandshakeMessage error: %v", err)
		}
	}()

	// Read the message on the server side.
	recvHandshake, recvSignature, err := ReceiveHandshakeMessage(serverConn)
	if err != nil {
		t.Fatalf("ReceiveHandshakeMessage error: %v", err)
	}

	if !bytes.Equal(handshakeBytes, recvHandshake) {
		t.Errorf("handshakeBytes mismatch: expected %q, got %q", handshakeBytes, recvHandshake)
	}
	if !bytes.Equal(signature, recvSignature) {
		t.Errorf("signature mismatch: expected %q, got %q", signature, recvSignature)
	}
}

func TestSendHandshakeMessageWriteError(t *testing.T) {
	conn := &configurableConn{
		failOnWrite: true,
		writeErr:    errors.New("simulated write error"),
	}
	handshakeBytes := []byte("handshake-data")
	signature := []byte("signature-data")

	err := SendHandshakeMessage(conn, handshakeBytes, signature)
	if err == nil {
		t.Fatal("expected an error but got nil")
	}
	// Check that the error message contains our simulated error text.
	if !strings.Contains(err.Error(), "simulated write error") {
		t.Fatalf("expected error to contain 'simulated write error', got: %v", err)
	}
}

// TestReceiveHandshakeMessageFailures combines several failure scenarios
// for ReceiveHandshakeMessage using a table-driven approach.
func TestReceiveHandshakeMessageFailures(t *testing.T) {
	// Define test cases.
	// Each test case simulates a different incomplete handshake message scenario.
	testCases := []struct {
		name          string
		conn          net.Conn // We'll use configurableConn.
		expectedError string
	}{
		{
			name: "fail to read handshake length",
			conn: &configurableConn{
				failOnRead: true,
				readErr:    errors.New("simulated read error"),
			},
			expectedError: "failed to read handshake length",
		},
		{
			name: "fail to read handshake bytes",
			conn: func() net.Conn {
				// Handshake length is declared as 10, but only 5 handshake bytes provided.
				handshakeLen := uint32(10)
				var buf bytes.Buffer

				lenBuf := make([]byte, MsgLenFieldSize)
				binary.BigEndian.PutUint32(lenBuf, handshakeLen)
				buf.Write(lenBuf)
				// Write only 5 bytes instead of 10.
				buf.Write([]byte("12345"))
				// Even if we provided signature part, the error will occur before that.
				return &configurableConn{readBuffer: bytes.NewBuffer(buf.Bytes())}
			}(),
			expectedError: "failed to read handshake bytes",
		},
		{
			name: "fail to read signature length",
			conn: func() net.Conn {
				// Provide valid handshake length and handshake bytes,
				// but omit the signature length.
				handshakeData := []byte("hello")
				handshakeLen := uint32(len(handshakeData))
				var buf bytes.Buffer

				lenBuf := make([]byte, MsgLenFieldSize)
				binary.BigEndian.PutUint32(lenBuf, handshakeLen)
				buf.Write(lenBuf)
				buf.Write(handshakeData)
				// Do not write signature length.
				return &configurableConn{readBuffer: bytes.NewBuffer(buf.Bytes())}
			}(),
			expectedError: "failed to read signature length",
		},
		{
			name: "fail to read signature",
			conn: func() net.Conn {
				// Provide valid handshake length and handshake bytes,
				// provide signature length as 10 but only 5 bytes of signature.
				handshakeData := []byte("hello")
				handshakeLen := uint32(len(handshakeData))

				signatureLen := uint32(10)
				signatureData := []byte("12345") // only 5 bytes

				var buf bytes.Buffer
				lenBuf := make([]byte, MsgLenFieldSize)

				// Write handshake length.
				binary.BigEndian.PutUint32(lenBuf, handshakeLen)
				buf.Write(lenBuf)
				buf.Write(handshakeData)

				// Write signature length.
				binary.BigEndian.PutUint32(lenBuf, signatureLen)
				buf.Write(lenBuf)
				// Write incomplete signature bytes.
				buf.Write(signatureData)
				return &configurableConn{readBuffer: bytes.NewBuffer(buf.Bytes())}
			}(),
			expectedError: "failed to read signature",
		},
	}

	// Execute each test case as a subtest.
	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			// Call the function under test.
			_, _, err := ReceiveHandshakeMessage(tc.conn)
			if err == nil {
				t.Fatalf("expected error but got nil")
			}
			if !strings.Contains(err.Error(), tc.expectedError) {
				t.Fatalf("expected error message to contain %q, got: %v", tc.expectedError, err)
			}
		})
	}
}

// TestParseValidHandshakeMessage verifies that ParseHandshakeMessage extracts the expected
// handshake info from a handshake message.
func TestParseValidHandshakeMessage(t *testing.T) {
	// Create a valid handshake message.
	validHandshake := &lumeraidtypes.HandshakeInfo{
		Address:   "127.0.0.1:8080",
		PeerType:  int32(securekeyx.Supernode),
		PublicKey: []byte("public-key"),
		Curve:     "curve-name",
	}
	handshakeBytes, err := proto.Marshal(validHandshake)
	if err != nil {
		t.Fatalf("proto.Marshal error: %v", err)
	}

	// Parse the handshake bytes.
	info, err := ParseHandshakeMessage(handshakeBytes)
	if err != nil {
		t.Fatalf("ParseRemoteAddress error: %v", err)
	}
	require.Equal(t, validHandshake.Address, info.Address, "address mismatch")
	require.Equal(t, validHandshake.PeerType, info.PeerType, "peer type mismatch")
	require.Equal(t, validHandshake.PublicKey, info.PublicKey, "public key mismatch")
	require.Equal(t, validHandshake.Curve, info.Curve, "curve mismatch")
}

// TestParseInvalidHandshakeMessage verifies that ParseHandshakeMessage returns an error
// when the handshake message is invalid.
func TestParseInvalidHandshakeMessage(t *testing.T) {
	// Create an invalid handshake message (empty bytes).
	invalidHandshake := []byte{}
	_, err := ParseHandshakeMessage(invalidHandshake)
	require.Error(t, err, "expected error but got nil")

	// Create an invalid handshake message (non-protobuf bytes).
	invalidHandshake = []byte("invalid-handshake")
	_, err = ParseHandshakeMessage(invalidHandshake)
	require.Error(t, err, "expected error but got nil")
}

// TestGetALTSKeySize tests that GetALTSKeySize returns the expected key sizes
// for supported protocols and an error for unsupported ones.
func TestGetALTSKeySize(t *testing.T) {
	// Test RecordProtocolAESGCM.
	keySize, err := GetALTSKeySize(RecordProtocolAESGCM)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if keySize != KeySizeAESGCM {
		t.Errorf("expected key size %d, got %d", KeySizeAESGCM, keySize)
	}

	// Test RecordProtocolAESGCMReKey.
	keySize, err = GetALTSKeySize(RecordProtocolAESGCMReKey)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if keySize != KeySizeAESGCMReKey {
		t.Errorf("expected key size %d, got %d", KeySizeAESGCMReKey, keySize)
	}

	// Test RecordProtocolXChaCha20Poly1305ReKey.
	keySize, err = GetALTSKeySize(RecordProtocolXChaCha20Poly1305ReKey)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if keySize != KeySizeXChaCha20Poly1305ReKey {
		t.Errorf("expected key size %d, got %d", KeySizeXChaCha20Poly1305ReKey, keySize)
	}

	// Test unsupported protocol.
	_, err = GetALTSKeySize("unsupported-protocol")
	if err == nil {
		t.Error("expected error for unsupported protocol, got nil")
	}
}

// TestExpandKey tests both branches of ExpandKey: when the shared secret is long enough to be sliced
// and when HKDF is used to expand a shorter shared secret.
func TestExpandKey(t *testing.T) {
	// Use protocol RecordProtocolAESGCM (assumed key size e.g. 16).
	protocol := RecordProtocolAESGCM
	keySize, err := GetALTSKeySize(protocol)
	if err != nil {
		t.Fatalf("GetALTSKeySize error: %v", err)
	}

	// Case 1: sharedSecret is long enough (simply sliced).
	sharedSecret := make([]byte, keySize+10)
	for i := range sharedSecret {
		sharedSecret[i] = byte(i)
	}
	expanded, err := ExpandKey(sharedSecret, protocol, []byte("info"))
	if err != nil {
		t.Fatalf("ExpandKey error: %v", err)
	}
	if len(expanded) != keySize {
		t.Errorf("expected expanded key length %d, got %d", keySize, len(expanded))
	}
	if !bytes.Equal(expanded, sharedSecret[:keySize]) {
		t.Error("expected expanded key to equal the first keySize bytes of sharedSecret")
	}

	// Case 2: sharedSecret is too short, so HKDF is used.
	shortSecret := []byte("short") // length less than keySize
	expandedHKDF, err := ExpandKey(shortSecret, protocol, []byte("info"))
	if err != nil {
		t.Fatalf("ExpandKey error: %v", err)
	}
	if len(expandedHKDF) != keySize {
		t.Errorf("expected expanded key length %d, got %d", keySize, len(expandedHKDF))
	}
	// Although not guaranteed, it is highly unlikely that the HKDF output will
	// start with the same bytes as shortSecret.
	if len(shortSecret) <= len(expandedHKDF) && bytes.Equal(expandedHKDF[:len(shortSecret)], shortSecret) {
		t.Error("expanded key appears to be a simple prefix of the shortSecret")
	}
	// Check that expansion is deterministic.
	expandedHKDF2, err := ExpandKey(shortSecret, protocol, []byte("info"))
	if err != nil {
		t.Fatalf("ExpandKey error: %v", err)
	}
	if !bytes.Equal(expandedHKDF, expandedHKDF2) {
		t.Error("ExpandKey is not deterministic for the same inputs")
	}

	// Test unsupported protocol.
	_, err = ExpandKey(sharedSecret, "unsupported-protocol", []byte("info"))
	if err == nil {
		t.Error("expected error for unsupported protocol, got nil")
	}
}

// TestSplitKeyAndNonce tests that SplitKeyAndNonce properly splits an expanded key
// for the XChaCha20Poly1305ReKey protocol.
func TestSplitKeyAndNonce(t *testing.T) {
	// Create a dummy expandedKey with the correct length.
	expandedKey := make([]byte, KeySizeXChaCha20Poly1305ReKey)
	for i := range expandedKey {
		expandedKey[i] = byte(i)
	}
	key, nonce := SplitKeyAndNonce(expandedKey)
	if key == nil || nonce == nil {
		t.Fatal("expected non-nil key and nonce")
	}
	if len(key) != 32 {
		t.Errorf("expected key length 32, got %d", len(key))
	}
	expectedNonceLen := KeySizeXChaCha20Poly1305ReKey - 32
	if len(nonce) != expectedNonceLen {
		t.Errorf("expected nonce length %d, got %d", expectedNonceLen, len(nonce))
	}

	// Test with an incorrectly sized expanded key.
	wrongSizeKey := make([]byte, 10)
	key, nonce = SplitKeyAndNonce(wrongSizeKey)
	if key != nil || nonce != nil {
		t.Error("expected nil key and nonce for incorrect expanded key length")
	}
}

// TestSplitKeyAndCounterMask tests that SplitKeyAndCounterMask properly splits an expanded key
// for the AESGCMReKey protocol.
func TestSplitKeyAndCounterMask(t *testing.T) {
	// Create a dummy expandedKey with the correct length.
	expandedKey := make([]byte, KeySizeAESGCMReKey)
	for i := range expandedKey {
		expandedKey[i] = byte(i)
	}
	key, counterMask := SplitKeyAndCounterMask(expandedKey)
	if key == nil || counterMask == nil {
		t.Fatal("expected non-nil key and counter mask")
	}
	if len(key) != 32 {
		t.Errorf("expected key length 32, got %d", len(key))
	}
	expectedMaskLen := KeySizeAESGCMReKey - 32
	if len(counterMask) != expectedMaskLen {
		t.Errorf("expected counter mask length %d, got %d", expectedMaskLen, len(counterMask))
	}

	// Test with an incorrectly sized expanded key.
	wrongSizeKey := make([]byte, 10)
	key, counterMask = SplitKeyAndCounterMask(wrongSizeKey)
	if key != nil || counterMask != nil {
		t.Error("expected nil key and counter mask for incorrect expanded key length")
	}
}
