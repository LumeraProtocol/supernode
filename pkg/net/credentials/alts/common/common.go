package common

const (
	// ClientSide identifies the client in this communication.
	ClientSide Side = iota
	// ServerSide identifies the server in this communication.
	ServerSide
)

// Side identifies the party's role: client or server.
type Side int

const (
	// MsgLenFieldSize is the byte size of the frame length field of a framed message.
	MsgLenFieldSize = 4

	LumeraALTSProtocol = "lumera-alts"

	// RecordProtocolName defines the supported record protocols
	// Record protocol using GCM-AES128
	RecordProtocolAESGCM = "ALTSRP_GCM_AES128"
	// Record protocol using GCM-AES128 with rekeying
	RecordProtocolAESGCMReKey = "ALTSRP_GCM_AES128_REKEY"
	// Record protocol using XChaCha20-Poly1305 with rekeying
	RecordProtocolXChaCha20Poly1305ReKey = "ALTSRP_XCHACHA20_POLY1305_REKEY"

	// Key sizes for different protocols
	KeySizeAESGCM                 = 16
	KeySizeAESGCMReKey            = 44 // 32 bytes key + 12 bytes counter mask
	KeySizeXChaCha20Poly1305ReKey = 56 // 32 bytes key + 24 bytes nonce
)

// ALTSRecordCrypto is the interface for gRPC ALTS record protocol.
type ALTSRecordCrypto interface {
	// Encrypt encrypts the plaintext and computes the tag (if any) of dst
	// and plaintext. dst and plaintext may fully overlap or not at all.
	Encrypt(dst, plaintext []byte) ([]byte, error)
	// EncryptionOverhead returns the tag size (if any) in bytes.
	EncryptionOverhead() int
	// Decrypt decrypts ciphertext and verify the tag (if any). dst and
	// ciphertext may alias exactly or not at all. To reuse ciphertext's
	// storage for the decrypted output, use ciphertext[:0] as dst.
	Decrypt(dst, ciphertext []byte) ([]byte, error)
}

// ALTSRecordFunc is a function type for factory functions that create
// ALTSRecordCrypto instances.
type ALTSRecordFunc func(s Side, keyData []byte) (ALTSRecordCrypto, error)
