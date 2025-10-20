package cascadekit

import (
	"bytes"

	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// ParseCompressedIndexFile parses a compressed index file into an IndexFile.
// The compressed format is: base64(IndexJSON).creator_signature.counter
func ParseCompressedIndexFile(data []byte) (IndexFile, error) {
	decompressed, err := utils.ZstdDecompress(data)
	if err != nil {
		return IndexFile{}, errors.Errorf("decompress index file: %w", err)
	}
	parts := bytes.Split(decompressed, []byte{SeparatorByte})
	if len(parts) != 3 {
		return IndexFile{}, errors.New("invalid index file format: expected 3 parts (index_b64.creator_sig_b64.counter)")
	}
	return DecodeIndexB64(string(parts[0]))
}
