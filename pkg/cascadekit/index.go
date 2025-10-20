package cascadekit

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

// SeparatorByte is the '.' separator used when composing payloads with counters.
const SeparatorByte byte = 46

// IndexFile represents the structure of the index file referenced on-chain.
// The JSON fields must match the existing format.
type IndexFile struct {
	Version         int      `json:"version,omitempty"`
	LayoutIDs       []string `json:"layout_ids"`
	LayoutSignature string   `json:"layout_signature"`
}

// BuildIndex creates an IndexFile from layout IDs and the layout signature.
func BuildIndex(layoutIDs []string, layoutSigB64 string) IndexFile {
	return IndexFile{LayoutIDs: layoutIDs, LayoutSignature: layoutSigB64}
}

// EncodeIndexB64 marshals an index file and returns its base64-encoded JSON.
func EncodeIndexB64(idx IndexFile) (string, error) {
	raw, err := json.Marshal(idx)
	if err != nil {
		return "", errors.Errorf("marshal index file: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeIndexB64 decodes base64(JSON(IndexFile)).
func DecodeIndexB64(data string) (IndexFile, error) {
	var indexFile IndexFile
	decodedData, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return indexFile, errors.Errorf("failed to decode index file: %w", err)
	}
	if err := json.Unmarshal(decodedData, &indexFile); err != nil {
		return indexFile, errors.Errorf("failed to unmarshal index file: %w", err)
	}
	return indexFile, nil
}

// ExtractIndexAndCreatorSig splits a signature-format string formatted as:
// Base64(index_json).Base64(creator_signature)
func ExtractIndexAndCreatorSig(indexSignatureFormat string) (indexB64 string, creatorSigB64 string, err error) {
	parts := strings.Split(indexSignatureFormat, ".")
	if len(parts) != 2 {
		return "", "", errors.New("invalid index signature format: expected 2 segments (index_b64.creator_sig_b64)")
	}
	return parts[0], parts[1], nil
}
