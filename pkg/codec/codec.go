package codec

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// EncodeResponse represents the response of the encode request.
// Layout contains the single-block layout produced by the encoder.
type EncodeResponse struct {
	Layout     Layout
	SymbolsDir string
}

type Layout struct {
	Blocks []Block `json:"blocks"`
}

// Block is the schema for each entry in the "blocks" array.
type Block struct {
	BlockID           int               `json:"block_id"`
	EncoderParameters EncoderParamsArray `json:"encoder_parameters"`
	OriginalOffset    int64             `json:"original_offset"`
	Size              int64             `json:"size"`
	Symbols           []string          `json:"symbols"`
	Hash              string            `json:"hash"`
}

// EncoderParamsArray is a custom type that marshals []uint8 as JSON array instead of base64 string.
// This ensures compatibility with WASM-generated layouts which use array format.
type EncoderParamsArray []uint8

// MarshalJSON implements json.Marshaler to serialize as array instead of base64
func (e EncoderParamsArray) MarshalJSON() ([]byte, error) {
	// Force array serialization by converting to []int
	arr := make([]int, len(e))
	for i, v := range e {
		arr[i] = int(v)
	}
	return json.Marshal(arr)
}

// UnmarshalJSON implements json.Unmarshaler to deserialize from either array or base64
func (e *EncoderParamsArray) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as array first (WASM format)
	var arr []int
	if err := json.Unmarshal(data, &arr); err == nil {
		*e = make([]uint8, len(arr))
		for i, v := range arr {
			(*e)[i] = uint8(v)
		}
		return nil
	}

	// Fallback: unmarshal as base64 string (old Go format, backward compatibility)
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	decoded, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return err
	}
	*e = decoded
	return nil
}

// EncodeRequest represents the request to encode a file.
type EncodeRequest struct {
	TaskID   string
	Path     string
	DataSize int
}
type CreateMetadataRequest struct {
	Path string
}

// CreateMetadataResponse returns the Layout.
type CreateMetadataResponse struct {
	Layout Layout
}

// RaptorQ contains methods for request services from RaptorQ service.
type Codec interface {
	// Encode a file
	Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error)
	// without generating RaptorQ symbols.
	CreateMetadata(ctx context.Context, req CreateMetadataRequest) (CreateMetadataResponse, error)
	PrepareDecode(ctx context.Context, actionID string, layout Layout) (
		blockPaths []string,
		Write func(block int, symbolID string, data []byte) (string, error),
		Cleanup func() error,
		ws *Workspace,
		err error,
	)
	DecodeFromPrepared(ctx context.Context, ws *Workspace, layout Layout) (DecodeResponse, error)
}
