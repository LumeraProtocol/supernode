package codec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	raptorq "github.com/LumeraProtocol/rq-go"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
)

type raptorQ struct {
	symbolsBaseDir string
}

func NewRaptorQCodec(dir string) Codec {
	return &raptorQ{
		symbolsBaseDir: dir,
	}

}

func (rq *raptorQ) Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error) {
	/* ---------- 1.  initialise RaptorQ processor ---------- */

	processor, err := raptorq.NewDefaultRaptorQProcessor()
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create RaptorQ processor: %w", err)
	}
	defer processor.Free()

	logtrace.Info(ctx, "RaptorQ processor created", logtrace.Fields{
		"data-size": len(req.Data)})
	/* ---------- 2.  persist req.Data to a temp file ---------- */

	tmp, err := os.CreateTemp("", "rq-encode-*")
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(req.Data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return EncodeResponse{}, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil { // sync to disk
		os.Remove(tmpPath)
		return EncodeResponse{}, fmt.Errorf("close temp file: %w", err)
	}

	/* ---------- 3.  run the encoder ---------- */

	blockSize := processor.GetRecommendedBlockSize(uint64(len(req.Data)))

	symbolsDir := filepath.Join(rq.symbolsBaseDir, req.TaskID)
	if err := os.MkdirAll(symbolsDir, 0o755); err != nil {
		os.Remove(tmpPath)
		return EncodeResponse{}, fmt.Errorf("mkdir %s: %w", symbolsDir, err)
	}

	logtrace.Info(ctx, "RaptorQ processor encoding", logtrace.Fields{
		"symbols-dir": symbolsDir,
		"temp-file":   tmpPath})

	resp, err := processor.EncodeFile(tmpPath, symbolsDir, blockSize)
	if err != nil {
		os.Remove(tmpPath)
		return EncodeResponse{}, fmt.Errorf("raptorq encode: %w", err)
	}

	/* we no longer need the temp file */
	// _ = os.Remove(tmpPath)

	/* ---------- 4.  read the layout JSON ---------- */
	layoutData, err := os.ReadFile(resp.LayoutFilePath)

	logtrace.Info(ctx, "RaptorQ processor layout file", logtrace.Fields{
		"layout-file": resp.LayoutFilePath})
	if err != nil {
		return EncodeResponse{}, fmt.Errorf("read layout %s: %w", resp.LayoutFilePath, err)
	}

	var encodeResp EncodeResponse
	if err := json.Unmarshal(layoutData, &encodeResp.Metadata); err != nil {
		return EncodeResponse{}, fmt.Errorf("unmarshal layout: %w", err)
	}
	encodeResp.SymbolsDir = symbolsDir

	return encodeResp, nil
}
