package common

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/cosmos/gogoproto/proto"
	"golang.org/x/crypto/hkdf"

	lumeraidtypes "github.com/LumeraProtocol/lumera/x/lumeraid/types"
)

type SendHandshakeMessageFunc func(conn net.Conn, handshakeBytes, signature []byte) error
type ReceiveHandshakeMessageFunc func(conn net.Conn) ([]byte, []byte, error)
type ParseHandshakeMessageFunc func(handshakeBytes []byte) (*lumeraidtypes.HandshakeInfo, error)
type ExpandKeyFunc func(sharedSecret []byte, protocol string, info []byte) ([]byte, error)

// defaultSendHandshakeMessage sends serialized handshake bytes with its signature over the connection
// Format: [handshake length][handshake bytes][signature length][signature]
func defaultSendHandshakeMessage(conn net.Conn, handshakeBytes, signature []byte) error {
	if handshakeBytes == nil {
		return fmt.Errorf("empty handshake")
	}
	// Normalise nil → empty slice so len() works and we still send the frame.
	if signature == nil {
		signature = []byte{}
	}

	// Calculate total message size and allocate a single buffer
	totalSize := MsgLenFieldSize + len(handshakeBytes) + MsgLenFieldSize + len(signature)
	buf := make([]byte, totalSize)

	// Write all data into the buffer
	offset := 0

	// Write handshake length
	binary.BigEndian.PutUint32(buf[offset:], uint32(len(handshakeBytes)))
	offset += MsgLenFieldSize

	// Write handshake bytes
	copy(buf[offset:], handshakeBytes)
	offset += len(handshakeBytes)

	// Write signature length
	binary.BigEndian.PutUint32(buf[offset:], uint32(len(signature)))
	offset += MsgLenFieldSize

	// Write signature
	copy(buf[offset:], signature)

	// Send the entire message in one write
	if _, err := conn.Write(buf); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// SendHandshakeMessage sends serialized handshake bytes with its signature over the connection.
// Format: [handshake length][handshake bytes][signature length][signature]
var SendHandshakeMessage SendHandshakeMessageFunc = defaultSendHandshakeMessage

const maxFrameSize = 64 * 1024 // 64 KiB is plenty for a protobuf handshake

// defaultReceiveHandshakeMessage receives handshake bytes and its signature from the connection.
// Format: [handshake length][handshake bytes][signature length][signature]
func defaultReceiveHandshakeMessage(conn net.Conn) ([]byte, []byte, error) {
	var lenBuf [MsgLenFieldSize]byte

	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, nil, fmt.Errorf("read handshake len: %w", err)
	}
	handshakeLen := binary.BigEndian.Uint32(lenBuf[:])
	if handshakeLen == 0 || handshakeLen > maxFrameSize {
		return nil, nil, fmt.Errorf("invalid handshake length %d", handshakeLen)
	}

	handshake := make([]byte, handshakeLen)
	if _, err := io.ReadFull(conn, handshake); err != nil {
		return nil, nil, fmt.Errorf("read handshake: %w", err)
	}

	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, nil, fmt.Errorf("read signature len: %w", err)
	}
	sigLen := binary.BigEndian.Uint32(lenBuf[:])
	if sigLen == 0 || sigLen > maxFrameSize {
		return nil, nil, fmt.Errorf("invalid signature length %d", sigLen)
	}

	signature := make([]byte, sigLen)
	if _, err := io.ReadFull(conn, signature); err != nil {
		return nil, nil, fmt.Errorf("read signature: %w", err)
	}

	return handshake, signature, nil
}

// receiveHandshakeMessage receives handshake bytes and its signature from the connection
// Format: [handshake length][handshake bytes][signature length][signature]
var ReceiveHandshakeMessage ReceiveHandshakeMessageFunc = defaultReceiveHandshakeMessage

func defaultParseHandshakeMessage(handshakeBytes []byte) (*lumeraidtypes.HandshakeInfo, error) {
	var handshake lumeraidtypes.HandshakeInfo
	// empty handshake bytes is invalid
	if len(handshakeBytes) == 0 {
		return nil, fmt.Errorf("empty handshake bytes")
	}
	if err := proto.Unmarshal(handshakeBytes, &handshake); err != nil {
		return nil, fmt.Errorf("failed to unmarshal handshake info: %w", err)
	}
	return &handshake, nil
}

// ParseHandshakeMessage extracts info from handshake bytes
var ParseHandshakeMessage ParseHandshakeMessageFunc = defaultParseHandshakeMessage

// GetALTSKeySize returns the key size for the given ALTS protocol
func GetALTSKeySize(protocol string) (int, error) {
	switch protocol {
	case RecordProtocolAESGCM:
		return KeySizeAESGCM, nil
	case RecordProtocolAESGCMReKey:
		return KeySizeAESGCMReKey, nil
	case RecordProtocolXChaCha20Poly1305ReKey:
		return KeySizeXChaCha20Poly1305ReKey, nil
	default:
		return 0, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

// defaultExpandKey always runs HKDF–SHA-256 so the key material is
// uniformly random even when the raw ECDH secret is longer than needed.
func defaultExpandKey(sharedSecret []byte, protocol string, info []byte) ([]byte, error) {
	keySize, err := GetALTSKeySize(protocol)
	if err != nil {
		return nil, err
	}

	// HKDF with empty salt (ok for ECDH) & caller-provided info.
	h := hkdf.New(sha256.New, sharedSecret, nil, info)

	key := make([]byte, keySize)
	if _, err := io.ReadFull(h, key); err != nil {
		return nil, fmt.Errorf("expand key: %w", err)
	}
	return key, nil
}

// ExpandKey derives protocol-specific keys from the shared secret using HKDF
var ExpandKey ExpandKeyFunc = defaultExpandKey

// For XChaCha20Poly1305ReKey, helper to split the expanded key into key and nonce
func SplitKeyAndNonce(expandedKey []byte) (key, nonce []byte) {
	if len(expandedKey) != KeySizeXChaCha20Poly1305ReKey {
		return nil, nil
	}
	return expandedKey[:32], expandedKey[32:]
}

// For AESGCMReKey, helper to split the expanded key into key and counter mask
func SplitKeyAndCounterMask(expandedKey []byte) (key, counterMask []byte) {
	if len(expandedKey) != KeySizeAESGCMReKey {
		return nil, nil
	}
	return expandedKey[:32], expandedKey[32:]
}
