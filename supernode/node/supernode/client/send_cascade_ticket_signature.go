package client

import (
	"context"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
)

// SendCascadeTicketSignature implements SendCascadeTicketSignature
func (service *SupernodeCascadeActionClient) SendCascadeTicketSignature(ctx context.Context, nodeID string, signature []byte, data []byte, rqFile []byte, rqEncodeParams raptorq.EncoderParameters) error {
	ctx = contextWithMDSessID(ctx, service.sessID)

	_, err := service.client.SendCascadeTicketSignature(ctx, &pb.SendTicketSignatureRequest{
		NodeID:    nodeID,
		Signature: signature,
		Data:      data,
		RqFile:    rqFile,
		RqEncodeParams: &pb.EncoderParameters{
			Oti: rqEncodeParams.Oti,
		},
	})

	return err
}
