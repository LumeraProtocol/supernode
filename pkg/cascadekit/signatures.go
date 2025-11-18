package cascadekit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"

	actionkeeper "github.com/LumeraProtocol/lumera/x/action/v1/keeper"

	keyringpkg "github.com/LumeraProtocol/supernode/v2/pkg/keyring"

	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// Signer is a function that signs the provided message and returns the raw signature bytes.
type Signer func(msg []byte) ([]byte, error)

// SignLayoutB64 validates single-block layout, marshals to JSON, base64-encodes it,
// and signs the base64 payload, returning both the layout base64 and signature base64.
//
// Message signed = layoutB64 string (same as JS layoutBytesB64 if layout JSON matches).
func SignLayoutB64(layout codec.Layout, signer Signer) (layoutB64 string, layoutSigB64 string, err error) {
	if len(layout.Blocks) != 1 {
		return "", "", errors.New("layout must contain exactly one block")
	}

	me, err := json.Marshal(layout)
	if err != nil {
		return "", "", errors.Errorf("marshal layout: %w", err)
	}
	layoutB64 = base64.StdEncoding.EncodeToString(me)

	sig, err := signer([]byte(layoutB64))
	if err != nil {
		return "", "", errors.Errorf("sign layout: %w", err)
	}
	layoutSigB64 = base64.StdEncoding.EncodeToString(sig)
	return layoutB64, layoutSigB64, nil
}

// SignIndexB64 marshals the index to JSON, base64-encodes it, and signs the
// JSON string (not the base64), returning both the index base64 and creator-signature base64.
//
// IMPORTANT:
//   - Message signed = index JSON string (same as JS signArbitrary(indexFileString))
//   - indexB64 is still base64(JSON(index)), used in metadata and RQID generation.
func SignIndexB64(idx IndexFile, signer Signer) (indexB64 string, creatorSigB64 string, err error) {
	raw, err := json.Marshal(idx)
	if err != nil {
		return "", "", errors.Errorf("marshal index file: %w", err)
	}

	indexJSON := string(raw)

	// Sign the JSON string (JS-style)
	sig, err := signer([]byte(indexJSON))
	if err != nil {
		return "", "", errors.Errorf("sign index: %w", err)
	}
	creatorSigB64 = base64.StdEncoding.EncodeToString(sig)

	// Base64(JSON(index)) used as the first segment of indexSignatureFormat
	indexB64 = base64.StdEncoding.EncodeToString(raw)
	return indexB64, creatorSigB64, nil
}

// CreateSignatures produces the index signature format and index IDs:
//
//	indexSignatureFormat = Base64(index_json) + "." + Base64(creator_signature)
//
// It validates the layout has exactly one block.
//
// The "signer" can be:
//   - raw: directly sign msg bytes (legacy Go path)
//   - ADR-36: wrap msg into an ADR-36 sign doc, then sign (JS-compatible path)
func CreateSignatures(layout codec.Layout, signer Signer, ic, max uint32) (indexSignatureFormat string, indexIDs []string, err error) {
	layoutB64, layoutSigB64, err := SignLayoutB64(layout, signer)
	if err != nil {
		return "", nil, err
	}

	// Generate layout IDs (not returned; used to populate the index file)
	layoutSignatureFormat := layoutB64 + "." + layoutSigB64
	layoutIDs, err := GenerateLayoutIDs(layoutSignatureFormat, ic, max)
	if err != nil {
		return "", nil, err
	}

	// Build and sign the index file (JS-style: message = index JSON string)
	idx := BuildIndex(layoutIDs, layoutSigB64)
	indexB64, creatorSigB64, err := SignIndexB64(idx, signer)
	if err != nil {
		return "", nil, err
	}
	indexSignatureFormat = fmt.Sprintf("%s.%s", indexB64, creatorSigB64)

	// Generate the index IDs (these are the RQIDs sent to chain)
	indexIDs, err = GenerateIndexIDs(indexSignatureFormat, ic, max)
	if err != nil {
		return "", nil, err
	}
	return indexSignatureFormat, indexIDs, nil
}

// CreateSignaturesWithKeyring signs layout and index using a Cosmos keyring (legacy path).
// Message signed = raw bytes passed by SignLayoutB64 / SignIndexB64:
//   - layout: layoutB64 string
//   - index:  index JSON string
//
// The verification pipeline already handles both raw and ADR-36, so this remains valid.
func CreateSignaturesWithKeyring(
	layout codec.Layout,
	kr sdkkeyring.Keyring,
	keyName string,
	ic, max uint32,
) (string, []string, error) {
	signer := func(msg []byte) ([]byte, error) {
		return keyringpkg.SignBytes(kr, keyName, msg)
	}
	return CreateSignatures(layout, signer, ic, max)
}

// adr36SignerForKeyring creates a signer that signs ADR-36 doc bytes
// for the given signer address. The "msg" we pass in is the *message*
// (layoutB64, index JSON, etc.), and this helper wraps it into ADR-36.
func adr36SignerForKeyring(
	kr sdkkeyring.Keyring,
	keyName string,
	signerAddr string,
) Signer {
	return func(msg []byte) ([]byte, error) {
		// msg is the cleartext message we want to sign (e.g., layoutB64 or index JSON string)
		dataB64 := base64.StdEncoding.EncodeToString(msg)

		// Build ADR-36 sign bytes: signerAddr + base64(message)
		doc, err := actionkeeper.MakeADR36AminoSignBytes(signerAddr, dataB64)
		if err != nil {
			return nil, err
		}

		// Now sign the ADR-36 doc bytes with the keyring (direct secp256k1)
		return keyringpkg.SignBytes(kr, keyName, doc)
	}
}

// CreateSignaturesWithKeyringADR36 creates signatures in the SAME way as the JS SDK:
//
//   - layout: Keplr-like ADR-36 signature over layoutB64 string
//   - index:  Keplr-like ADR-36 signature over index JSON string
//
// The resulting indexSignatureFormat string will match what JS produces for the same
// layout, signer, ic, and max.
func CreateSignaturesWithKeyringADR36(
	layout codec.Layout,
	kr sdkkeyring.Keyring,
	keyName string,
	ic, max uint32,
) (string, []string, error) {
	// Resolve signer bech32 address from keyring
	addr, err := keyringpkg.GetAddress(kr, keyName)
	if err != nil {
		return "", nil, fmt.Errorf("resolve signer address: %w", err)
	}

	signer := adr36SignerForKeyring(kr, keyName, addr.String())

	return CreateSignatures(layout, signer, ic, max)
}

// SignADR36String signs a message string using the ADR-36 scheme that Keplr uses.
// "message" must be the same string you'd pass to Keplr's signArbitrary, e.g.:
//   - layoutB64
//   - index JSON
//   - dataHash (base64 blake3)
func SignADR36String(
	kr sdkkeyring.Keyring,
	keyName string,
	signerAddr string,
	message string,
) (string, error) {
	// 1) message -> []byte
	msgBytes := []byte(message)

	// 2) base64(UTF-8(message))
	dataB64 := base64.StdEncoding.EncodeToString(msgBytes)

	// 3) Build ADR-36 sign bytes (Keplr-accurate)
	docBytes, err := actionkeeper.MakeADR36AminoSignBytes(signerAddr, dataB64)
	if err != nil {
		return "", fmt.Errorf("build adr36 sign bytes: %w", err)
	}

	// 4) Sign with Cosmos keyring
	sig, err := keyringpkg.SignBytes(kr, keyName, docBytes)
	if err != nil {
		return "", fmt.Errorf("sign adr36 doc: %w", err)
	}

	// 5) Wire format: base64(rsSignature)
	return base64.StdEncoding.EncodeToString(sig), nil
}
