package adaptors

import (
	"context"
	"os"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
)

// CodecService wraps codec operations used by cascade
type CodecService interface {
	EncodeInput(ctx context.Context, actionID string, filePath string) (EncodeResult, error)
	Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error)
}

type EncodeResult struct {
	SymbolsDir string
	Layout     codec.Layout
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

func (c *codecImpl) EncodeInput(ctx context.Context, actionID, filePath string) (EncodeResult, error) {
	var size int
	if fi, err := os.Stat(filePath); err == nil {
		size = int(fi.Size())
	}
	res, err := c.codec.Encode(ctx, codec.EncodeRequest{TaskID: actionID, Path: filePath, DataSize: size})
	if err != nil {
		return EncodeResult{}, err
	}
	return EncodeResult{SymbolsDir: res.SymbolsDir, Layout: res.Layout}, nil
}

func (c *codecImpl) Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error) {
	res, err := c.codec.Decode(ctx, codec.DecodeRequest{ActionID: req.ActionID, Symbols: req.Symbols, Layout: req.Layout})
	if err != nil {
		return DecodeResult{}, err
	}
	return DecodeResult{FilePath: res.FilePath, DecodeTmpDir: res.DecodeTmpDir}, nil
}
