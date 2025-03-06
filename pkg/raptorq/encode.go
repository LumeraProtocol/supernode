package raptorq

import (
	"context"
	"fmt"

	rq "github.com/LumeraProtocol/rq-service/gen"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type EncodeRequest struct {
	Path string
}

type EncodeResponse struct {
	EncoderParameters []byte
	SymbolsCount      uint32
	Path              string
}

// Encode handles data encoding
func (c *Client) Encode(ctx context.Context, req EncodeRequest) (EncodeResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "Encode",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "encoding metadata", fields)

	res, err := c.rqService.Encode(ctx, &rq.EncodeRequest{Path: req.Path})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to encode data", fields)
		return EncodeResponse{}, fmt.Errorf("raptorQ encode error: %w", err)
	}

	logtrace.Info(ctx, "successfully encoded data", fields)
	return toEncodedResponse(res), nil
}

func toEncodedResponse(reply *rq.EncodeReply) EncodeResponse {
	return EncodeResponse{
		EncoderParameters: reply.EncoderParameters,
		SymbolsCount:      reply.SymbolsCount,
		Path:              reply.Path,
	}
}
