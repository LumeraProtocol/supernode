package adaptors

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
)

// CodecService wraps codec operations used by cascade
type CodecService interface {
	EncodeInput(ctx context.Context, actionID string, path string) (EncodeResult, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error)
}

type EncodeResult struct {
	SymbolsDir string
	Metadata   codec.Layout
}

type DecodeRequest struct {
	ActionID string
	Symbols  map[string][]byte
	Layout   codec.Layout
}

type DecodeResult struct {
	FilePath     string
	DecodeTmpDir string
}

type codecImpl struct{ codec codec.Codec }

func NewCodecService(c codec.Codec) CodecService { return &codecImpl{codec: c} }

func (c *codecImpl) EncodeInput(ctx context.Context, actionID, path string) (EncodeResult, error) {
	res, err := c.codec.Encode(ctx, codec.EncodeRequest{TaskID: actionID, Path: path})
	if err != nil {
		return EncodeResult{}, err
	}
	return EncodeResult{SymbolsDir: res.SymbolsDir, Metadata: res.Metadata}, nil
}

func (c *codecImpl) Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error) {
	res, err := c.codec.Decode(ctx, codec.DecodeRequest{ActionID: req.ActionID, Symbols: req.Symbols, Layout: req.Layout})
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{FilePath: res.FilePath, DecodeTmpDir: res.DecodeTmpDir}, nil
}
