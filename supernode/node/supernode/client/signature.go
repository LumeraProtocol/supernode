package client

import (
	"context"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
)

// SendCascadeTicketSignature implements SendCascadeTicketSignature
func (service *RegisterCascade) SendCascadeTicketSignature(ctx context.Context, nodeID string, signature []byte) error {
	_, err := service.client.SendCascadeTicketSignature(ctx, &pb.SendTicketSignatureRequest{
		NodeID:    nodeID,
		Signature: signature,
	})

	return err
}
