package adaptors

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/codec"
)

// CodecService defines the interface for RaptorQ encoding of input data.
//
//go:generate mockgen -destination=mocks/rq_mock.go -package=cascadeadaptormocks -source=rq.go
type CodecService interface {
	EncodeInput(ctx context.Context, taskID string, path string, dataSize int) (EncodeResult, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error)
}

// EncodeResult represents the outcome of encoding the input data.
type EncodeResult struct {
	SymbolsDir string
	Metadata   codec.Layout
}

// codecImpl is the default implementation using the real codec service.
type codecImpl struct {
	codec codec.Codec
}

// NewCodecService creates a new production instance of CodecService.
func NewCodecService(codec codec.Codec) CodecService {
	return &codecImpl{codec: codec}
}

// EncodeInput encodes the provided data and returns symbols and metadata.
func (c *codecImpl) EncodeInput(ctx context.Context, taskID string, path string, dataSize int) (EncodeResult, error) {
	resp, err := c.codec.Encode(ctx, codec.EncodeRequest{
		TaskID:   taskID,
		Path:     path,
		DataSize: dataSize,
	})
	if err != nil {
		return EncodeResult{}, err
	}

	return EncodeResult{
		SymbolsDir: resp.SymbolsDir,
		Metadata:   resp.Metadata,
	}, nil
}

type DecodeRequest struct {
	Symbols  map[string][]byte
	Layout   codec.Layout
	ActionID string
}

type DecodeResponse struct {
	DecodeTmpDir string
	FilePath     string
}

// Decode decodes the provided symbols and returns the original file
func (c *codecImpl) Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error) {
	resp, err := c.codec.Decode(ctx, codec.DecodeRequest{
		Symbols:  req.Symbols,
		Layout:   req.Layout,
		ActionID: req.ActionID,
	})
	if err != nil {
		return DecodeResponse{}, err
	}

	return DecodeResponse{
		FilePath:     resp.Path,
		DecodeTmpDir: resp.DecodeTmpDir,
	}, nil
}
