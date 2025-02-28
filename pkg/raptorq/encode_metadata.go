package raptorq

import (
	"context"

	rq "github.com/LumeraProtocol/rq-service"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type EncodeMetadataRequest struct {
	Path        string
	FilesNumber uint32
	BlockHash   string
	PastelId    string
}

// EncodeMetaData handles encoding metadata
func (c *Client) EncodeMetaData(ctx context.Context, req EncodeMetadataRequest) (EncodeResponse, error) {
	ctx = net.AddCorrelationID(ctx)
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "EncodeMetaData",
		logtrace.FieldRequest: req,
	}
	logtrace.Info(ctx, "encoding metadata", fields)

	res, err := c.rqService.EncodeMetaData(ctx, &rq.EncodeMetaDataRequest{
		Path:        req.Path,
		FilesNumber: req.FilesNumber,
		BlockHash:   req.BlockHash,
		PastelId:    req.PastelId,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "error encoding metadata", fields)
		return EncodeResponse{}, nil
	}

	logtrace.Info(ctx, "successfully encoded metadata", fields)
	return toEncodedMetadataResponse(res), nil
}

func toEncodedMetadataResponse(reply *rq.EncodeMetaDataReply) EncodeResponse {
	return EncodeResponse{
		EncoderParameters: reply.EncoderParameters,
		SymbolsCount:      reply.SymbolsCount,
		Path:              reply.Path,
	}
}
