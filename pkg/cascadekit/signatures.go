package cascadekit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
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
// base64 payload, returning both the index base64 and creator-signature base64.
//
// IMPORTANT:
//   - Message signed = indexB64 string (chain-compatible)
//   - indexB64 is base64(JSON(index)), used in metadata and RQID generation.
func SignIndexB64(idx IndexFile, signer Signer) (indexB64 string, creatorSigB64 string, err error) {
	raw, err := json.Marshal(idx)
	if err != nil {
		return "", "", errors.Errorf("marshal index file: %w", err)
	}

	indexB64 = base64.StdEncoding.EncodeToString(raw)

	// Sign the base64 payload (chain-compatible)
	sig, err := signer([]byte(indexB64))
	if err != nil {
		return "", "", errors.Errorf("sign index: %w", err)
	}
	creatorSigB64 = base64.StdEncoding.EncodeToString(sig)

	return indexB64, creatorSigB64, nil
}

// CreateSignatures produces the index signature format and index IDs:
//
//	indexSignatureFormat = Base64(index_json) + "." + Base64(creator_signature)
//
// It validates the layout has exactly one block.
//
// The signer directly signs the message bytes (raw signing).
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

	// Build and sign the index file (message = indexB64 string)
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

// CreateSignaturesWithKeyring signs layout and index using a Cosmos keyring.
// Message signed = raw bytes passed by SignLayoutB64 / SignIndexB64:
//   - layout: layoutB64 string
//   - index:  index JSON string
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
