package common

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"crypto/sha256"

	"golang.org/x/crypto/hkdf"
	"github.com/cosmos/gogoproto/proto"

	lumeraidtypes "github.com/LumeraProtocol/lumera/x/lumeraid/types"
)

type SendHandshakeMessageFunc func(conn net.Conn, handshakeBytes, signature []byte) error
type ReceiveHandshakeMessageFunc func(conn net.Conn) ([]byte, []byte, error)
type ParseHandshakeMessageFunc func(handshakeBytes []byte) (*lumeraidtypes.HandshakeInfo, error)
type ExpandKeyFunc func(sharedSecret []byte, protocol string, info []byte) ([]byte, error)

// defaultSendHandshakeMessage sends serialized handshake bytes with its signature over the connection
// Format: [handshake length][handshake bytes][signature length][signature]
func defaultSendHandshakeMessage(conn net.Conn, handshakeBytes, signature []byte) error {
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

// defaultReceiveHandshakeMessage receives handshake bytes and its signature from the connection.
// Format: [handshake length][handshake bytes][signature length][signature]
func defaultReceiveHandshakeMessage(conn net.Conn) ([]byte, []byte, error) {
	// Read handshake length
	lenBuf := make([]byte, MsgLenFieldSize)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, nil, fmt.Errorf("failed to read handshake length: %w", err)
	}
	handshakeLen := binary.BigEndian.Uint32(lenBuf)

	// Read handshake bytes
	handshakeBytes := make([]byte, handshakeLen)
	if _, err := io.ReadFull(conn, handshakeBytes); err != nil {
		return nil, nil, fmt.Errorf("failed to read handshake bytes: %w", err)
	}

	// Read signature length
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, nil, fmt.Errorf("failed to read signature length: %w", err)
	}
	sigLen := binary.BigEndian.Uint32(lenBuf)

	// Read signature
	signature := make([]byte, sigLen)
	if _, err := io.ReadFull(conn, signature); err != nil {
		return nil, nil, fmt.Errorf("failed to read signature: %w", err)
	}

	return handshakeBytes, signature, nil
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

func defaultExpandKey(sharedSecret []byte, protocol string, info []byte) ([]byte, error) {
	keySize, err := GetALTSKeySize(protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to get key size: %w", err)
	}
	if (keySize <= len(sharedSecret)) {
		return sharedSecret[:keySize], nil
	}

    // Use HKDF with SHA-256
    hkdf := hkdf.New(sha256.New, sharedSecret, nil, info)
    
    key := make([]byte, keySize)
    if _, err := io.ReadFull(hkdf, key); err != nil {
        return nil, fmt.Errorf("failed to expand key: %w", err)
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
