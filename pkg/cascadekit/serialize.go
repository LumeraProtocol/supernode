package cascadekit

import (
	"encoding/base64"
	"encoding/json"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"
)

// LayoutJSON marshals a codec.Layout using the standard library encoder.
func LayoutJSON(layout codec.Layout) ([]byte, error) {
	b, err := json.Marshal(layout)
	if err != nil {
		return nil, errors.Errorf("marshal layout: %w", err)
	}
	return b, nil
}

// LayoutB64 returns base64(JSON(layout)) bytes using encoding/json for deterministic output.
func LayoutB64(layout codec.Layout) ([]byte, error) {
	raw, err := LayoutJSON(layout)
	if err != nil {
		return nil, err
	}
	out := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(out, raw)
	return out, nil
}
