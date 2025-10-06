package cascadekit

import (
	"bytes"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	json "github.com/json-iterator/go"
)

// ParseRQMetadataFile parses a compressed rq metadata file into layout, signature and counter.
// File format: base64(JSON(layout)).signature.counter (all parts separated by '.')
func ParseRQMetadataFile(data []byte) (layout codec.Layout, signature string, counter string, err error) {
	decompressed, err := utils.ZstdDecompress(data)
	if err != nil {
		return layout, "", "", errors.Errorf("decompress rq metadata file: %w", err)
	}

	// base64EncodeMetadata.Signature.Counter
	parts := bytes.Split(decompressed, []byte{SeparatorByte})
	if len(parts) != 3 {
		return layout, "", "", errors.New("invalid rq metadata format: expecting 3 parts (layout, signature, counter)")
	}

	layoutJson, err := utils.B64Decode(parts[0])
	if err != nil {
		return layout, "", "", errors.Errorf("base64 decode failed: %w", err)
	}

	if err := json.Unmarshal(layoutJson, &layout); err != nil {
		return layout, "", "", errors.Errorf("unmarshal layout: %w", err)
	}

	signature = string(parts[1])
	counter = string(parts[2])

	return layout, signature, counter, nil
}
