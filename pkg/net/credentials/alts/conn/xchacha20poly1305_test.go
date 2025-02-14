package conn

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"

	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

// getChaCha20Poly1305RekeyCryptoPair outputs a client/server pair on xchacha20poly1305ietfReKey.
func getChaCha20Poly1305RekeyCryptoPair(key []byte, t *testing.T) (ALTSRecordCrypto, ALTSRecordCrypto) {
	client, err := NewXChaCha20Poly1305ReKey(ClientSide, key)
	if err != nil {
		t.Fatalf("NewAES128GCMRekey(ClientSide, key) = %v", err)
	}
	server, err := NewXChaCha20Poly1305ReKey(ServerSide, key)
	if err != nil {
		t.Fatalf("NewAES128GCMRekey(ServerSide, key) = %v", err)
	}
	return client, server
}

func testRekeyEncryptRoundtripXchacha(client, server ALTSRecordCrypto, t *testing.T) {
	// Encrypt message with client write key
	msg := []byte("hello world")
	ciphertext, err := client.Encrypt(nil, msg)
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt message with server write key
	msg2 := []byte("hello world 2")
	ciphertext2, err := server.Encrypt(nil, msg2)
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt message with server read key
	plaintext, err := server.Decrypt(nil, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, msg) {
		t.Errorf("got plaintext = %q, want %q", plaintext, msg)
	}

	// Decrypt message with client read key
	plaintext2, err := client.Decrypt(nil, ciphertext2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext2, msg2) {
		t.Errorf("got plaintext = %q, want %q", plaintext2, msg2)
	}
}

// Test encrypt and decrypt on roundtrip messages for xchacha20poly1305ietfReKey.
func TestChaCha20Poly1305RekeyEncryptRoundtrip(t *testing.T) {
	// Test for xchacha20poly1305ietfReKey
	key := make([]byte, chacha20poly1305.KeySize+chacha20poly1305.NonceSizeX)
	client, server := getChaCha20Poly1305RekeyCryptoPair(key, t)
	testRekeyEncryptRoundtrip(client, server, t)
}

// Test encrypt and decrypt on roundtrip messages for xchacha20poly1305ietfReKey
// with a short key.
func TestChaCha20Poly1305InvalidKeyLength(t *testing.T) {
	key := make([]byte, chacha20poly1305.KeySize) // Too short
	_, err := NewXChaCha20Poly1305ReKey(ClientSide, key)
	if err == nil {
		t.Error("expected error for invalid key length, got nil")
	}
}
