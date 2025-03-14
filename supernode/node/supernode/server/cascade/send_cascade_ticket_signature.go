package cascade

import (
	"context"

	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	sc "github.com/LumeraProtocol/supernode/supernode/services/common"
)

// SendCascadeTicketSignature implements supernode.RegisterCascadeServer.SendCascadeTicketSignature()
func (service *RegisterCascade) SendCascadeTicketSignature(ctx context.Context, req *pb.SendTicketSignatureRequest) (*pb.SendTicketSignatureReply, error) {
	log.WithContext(ctx).WithField("req", req).Debugf("SendCascadeTicketSignature request")
	task, err := service.TaskFromMD(ctx)
	if err != nil {
		return nil, err
	}

	// TODO : add rq file to req, also confirm which func to call
	err = task.ValidateSignedTicketFromSecondaryNode(ctx, req.Data, req.NodeID, req.Signature, req.RqFile)

	if err := task.AddPeerTicketSignature(req.NodeID, req.Signature, sc.StatusAssetUploaded); err != nil {
		return nil, errors.Errorf("add peer signature %w", err)
	}

	return &pb.SendTicketSignatureReply{}, nil
}
