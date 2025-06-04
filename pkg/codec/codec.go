//go:generate mockgen -destination=codec_mock.go -package=codec -source=codec.go

package codec

import (
	"context"
)

// EncodeResponse  represents the response of the encode request.
type EncodeResponse struct {
	Metadata   Layout
	SymbolsDir string
}

type Layout struct {
	Blocks []Block `json:"blocks"`
}

// Block is the schema for each entry in the “blocks” array.
type Block struct {
	BlockID           int      `json:"block_id"`
	EncoderParameters []int    `json:"encoder_parameters"`
	OriginalOffset    int64    `json:"original_offset"`
	Size              int64    `json:"size"`
	Symbols           []string `json:"symbols"`
	Hash              string   `json:"hash"`
}

// EncodeRequest represents the request to encode a file.
type EncodeRequest struct {
	TaskID   string
	Path     string
	DataSize int
}

// RaptorQ contains methods for request services from RaptorQ service.
type Codec interface {
	// Encode a file
	Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error)
}
