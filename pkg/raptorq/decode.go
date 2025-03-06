package raptorq

import (
	"context"
	"fmt"

	rq "github.com/LumeraProtocol/rq-service/gen"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type DecodeRequest struct {
	EncoderParameters []byte
	Path              string
}

type DecodeResponse struct {
	SymbolsCount uint32
	Path         string
}

// Decode handles data decoding
func (c *Client) Decode(ctx context.Context, req DecodeRequest) (DecodeResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "Decode",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "decoding data", fields)

	res, err := c.rqService.Decode(ctx, &rq.DecodeRequest{EncoderParameters: req.EncoderParameters, Path: req.Path})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to decode data", fields)
		return DecodeResponse{}, fmt.Errorf("raptorQ decode error: %w", err)
	}

	logtrace.Info(ctx, "successfully decoded data", fields)
	return toDecodedResponse(res), nil
}

func toDecodedResponse(reply *rq.DecodeReply) DecodeResponse {
	return DecodeResponse{
		Path: reply.Path,
	}
}
