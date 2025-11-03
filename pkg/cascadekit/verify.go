package cascadekit

import (
	"encoding/base64"
	"fmt"

	actionkeeper "github.com/LumeraProtocol/lumera/x/action/v1/keeper"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

// Verifier is a function that verifies the signature over data using the signer's on-chain pubkey.
// It should return nil if signature is valid; otherwise an error.
type Verifier func(data []byte, signature []byte) error

// VerifyStringRawOrADR36 verifies a signature over a message string in two passes:
//  1. raw:   verify([]byte(message), sigRS)
//  2. ADR-36: build amino-JSON sign bytes with data = base64(message) and verify
//
// The signature is provided as base64 (DER or 64-byte r||s), and coerced to 64-byte r||s.
func VerifyStringRawOrADR36(message string, sigB64 string, signer string, verify Verifier) error {
	sigRaw, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}
	sigRS, err := actionkeeper.CoerceToRS64(sigRaw)
	if err != nil {
		return fmt.Errorf("coerce signature: %w", err)
	}
	if err := verify([]byte(message), sigRS); err == nil {
		return nil
	}
	dataB64 := base64.StdEncoding.EncodeToString([]byte(message))
	doc, err := actionkeeper.MakeADR36AminoSignBytes(signer, dataB64)
	if err != nil {
		return fmt.Errorf("build adr36 doc: %w", err)
	}
	if err := verify(doc, sigRS); err == nil {
		return nil
	}
	return fmt.Errorf("signature verification failed")
}

// VerifyIndex verifies the creator's signature over indexB64 (string), using the given verifier.
func VerifyIndex(indexB64 string, sigB64 string, signer string, verify Verifier) error {
	return VerifyStringRawOrADR36(indexB64, sigB64, signer, verify)
}

// VerifyLayout verifies the layout signature over base64(JSON(layout)) bytes.
func VerifyLayout(layoutB64 []byte, sigB64 string, signer string, verify Verifier) error {
	return VerifyStringRawOrADR36(string(layoutB64), sigB64, signer, verify)
}

// VerifySingleBlock ensures the RaptorQ layout contains exactly one block.
func VerifySingleBlock(layout codec.Layout) error {
	if len(layout.Blocks) != 1 {
		return errors.New("layout must contain exactly one block")
	}
	return nil
}
