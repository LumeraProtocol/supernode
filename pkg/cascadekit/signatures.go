package cascadekit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

// Signer is a function that signs the provided message and returns the raw signature bytes.
type Signer func(msg []byte) ([]byte, error)

// SignLayoutB64 validates single-block layout, marshals to JSON, base64-encodes it,
// and signs the base64 payload, returning both the layout base64 and signature base64.
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

// CreateSignatures reproduces the cascade signature format and index IDs:
//
//	Base64(index_json).Base64(creator_signature)
//
// It validates the layout has exactly one block.
func CreateSignatures(layout codec.Layout, signer Signer, ic, max uint32) (signatures string, indexIDs []string, err error) {
	layoutB64, layoutSigB64, err := SignLayoutB64(layout, signer)
	if err != nil {
		return "", nil, err
	}

	// Generate layout IDs (not returned; used to populate the index file)
	layoutIDs := GenerateLayoutIDs(layoutB64, layoutSigB64, ic, max)

	// Build and sign the index file
	idx := BuildIndex(layoutIDs, layoutSigB64)
	indexB64, _, err := EncodeIndexB64(idx)
	if err != nil {
		return "", nil, err
	}

	creatorSig, err := signer([]byte(indexB64))
	if err != nil {
		return "", nil, errors.Errorf("sign index: %w", err)
	}
	creatorSigB64 := base64.StdEncoding.EncodeToString(creatorSig)
	signatures = fmt.Sprintf("%s.%s", indexB64, creatorSigB64)

	// Generate the index IDs (these are the RQIDs sent to chain)
	indexIDs = GenerateIndexIDs(signatures, ic, max)
	return signatures, indexIDs, nil
}
