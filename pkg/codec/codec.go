package codec

import (
	"context"
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

// Block is the schema for each entry in the “blocks” array.
type Block struct {
	BlockID           int      `json:"block_id"`
	EncoderParameters []uint8  `json:"encoder_parameters"`
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
    // Streaming decode helpers for writing symbols directly to disk prior to decode
    PrepareDecode(ctx context.Context, actionID string, layout Layout) (blockPaths []string,
        Write func(block int, symbolID string, data []byte) (string, error), Cleanup func() error, ws *Workspace, err error)
    DecodeFromPrepared(ctx context.Context, ws *Workspace, layout Layout) (DecodeResponse, error)
}
