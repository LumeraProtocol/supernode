package system

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/cosmos/btcutil/base58"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"lukechampine.com/blake3"
)

func createCascadeLayoutSignature(metadataFile codec.Layout, kr cosmoskeyring.Keyring, userKeyName string, ic uint32, maxFiles uint32) (signatureFormat string, indexFileIDs []string, err error) {

	// Step 1: Convert metadata to JSON then base64
	me, err := json.Marshal(metadataFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	layoutBase64 := base64.StdEncoding.EncodeToString(me)

	// Step 2: Sign the layout data
	layoutSignature, err := keyring.SignBytes(kr, userKeyName, []byte(layoutBase64))
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign layout: %w", err)
	}
	layoutSignatureB64 := base64.StdEncoding.EncodeToString(layoutSignature)

	// Step 3: Generate redundant layout file IDs
	layoutIDs := generateLayoutIDsBatch(layoutBase64, layoutSignatureB64, ic, maxFiles)

	// Step 4: Create index file containing layout references
	indexFile := map[string]interface{}{
		"layout_ids":       layoutIDs,
		"layout_signature": layoutSignatureB64,
	}

	// Step 5: Sign the index file
	indexFileJSON, err := json.Marshal(indexFile)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal index file: %w", err)
	}
	indexFileBase64 := base64.StdEncoding.EncodeToString(indexFileJSON)

	creatorSignature, err := keyring.SignBytes(kr, userKeyName, []byte(indexFileBase64))
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign index file: %w", err)
	}
	creatorSignatureB64 := base64.StdEncoding.EncodeToString(creatorSignature)

	// Step 6: Create final signature format
	signatureFormat = fmt.Sprintf("%s.%s", indexFileBase64, creatorSignatureB64)

	// Step 7: Generate final index file IDs for submission
	indexFileIDs = generateIndexIDsBatch(signatureFormat, ic, maxFiles)

	return signatureFormat, indexFileIDs, nil
}

// Process: combine data -> add counter -> compress -> hash -> Base58 encode
func generateLayoutIDsBatch(layoutBase64, layoutSignatureB64 string, ic, maxFiles uint32) []string {
	layoutWithSig := fmt.Sprintf("%s.%s", layoutBase64, layoutSignatureB64)
	layoutIDs := make([]string, maxFiles)

	var buffer bytes.Buffer
	buffer.Grow(len(layoutWithSig) + 10)

	for i := uint32(0); i < maxFiles; i++ {
		// Build unique content with counter
		buffer.Reset()
		buffer.WriteString(layoutWithSig)
		buffer.WriteByte('.')
		buffer.WriteString(fmt.Sprintf("%d", ic+i))

		// Compress for efficiency
		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			continue
		}

		// Hash for uniqueness
		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			continue
		}

		// Base58 encode for readable ID
		layoutIDs[i] = base58.Encode(hash)
	}

	return layoutIDs
}

// generateIndexIDsBatch generates index file IDs using same process as layout IDs
func generateIndexIDsBatch(signatureFormat string, ic, maxFiles uint32) []string {
	indexFileIDs := make([]string, maxFiles)

	var buffer bytes.Buffer
	buffer.Grow(len(signatureFormat) + 10)

	for i := uint32(0); i < maxFiles; i++ {
		buffer.Reset()
		buffer.WriteString(signatureFormat)
		buffer.WriteByte('.')
		buffer.WriteString(fmt.Sprintf("%d", ic+i))

		compressedData, err := utils.ZstdCompress(buffer.Bytes())
		if err != nil {
			continue
		}
		hash, err := utils.Blake3Hash(compressedData)
		if err != nil {
			continue
		}
		indexFileIDs[i] = base58.Encode(hash)
	}
	return indexFileIDs
}

// ComputeBlake3Hash computes Blake3 hash of the given message
func ComputeBlake3Hash(msg []byte) ([]byte, error) {
	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, bytes.NewReader(msg)); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}
