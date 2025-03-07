package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/supernode"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UploadInputDataRequest struct {
	ActionID string
	Filename string
	DataHash string
	RqIc     int32
	RqMax    int32
}

type UploadInputDataResponse struct {
	Success bool
	Message string
}

func (task *CascadeRegistrationTask) UploadInputData(ctx context.Context, req *UploadInputDataRequest) (*UploadInputDataResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldRequest: req,
	}
	action, err := task.actionClient.GetAction(ctx, action.GetActionRequest{
		ActionID: req.ActionID,
		Type:     action.CascadeActionType,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get action", fields)
		return nil, status.Errorf(codes.Internal, "failed to get action")
	}
	if action.ActionID == "" {
		logtrace.Error(ctx, "action id not found", fields)
		return nil, status.Errorf(codes.Internal, "action id not found")
	}

	latestBlock, err := task.lumeraClient.GetLatestBlock(ctx)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get latest block", fields)
		return nil, status.Errorf(codes.Internal, "failed to get latest block")
	}

	// Verify latest block is in the top 10 Supernodes
	topSNsRes, err := task.supernodeClient.GetTopSNsByBlockHeight(ctx, supernode.GetTopSupernodesForBlockRequest{
		BlockHeight: int32(latestBlock.Height),
		Limit:       10,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get top SNs", fields)
		return nil, status.Errorf(codes.Internal, "failed to get top SNs")
	}
	var found bool
	for _, sn := range topSNsRes.Supernodes {
		if sn.SupernodeAccount == task.GetSNAddress() {
			found = true
			break
		}
	}
	if !found {
		logtrace.Error(ctx, "not a valid supernode", fields)
		return nil, status.Errorf(codes.Internal, "not a valid supernode")
	}

	if req.DataHash != action.Metadata.GetCascadeMetadata().DataHash {
		logtrace.Error(ctx, "data hash doesn't match", fields)
		return nil, status.Errorf(codes.Internal, "data hash doesn't match")
	}

	// FIXME : use proper file
	task.RQIDsIc, task.RQIDs, task.RQIDsFile, _, task.creatorSignature, err = task.rqClient.GenRQIdentifiersFiles(ctx, fields, nil, string(latestBlock.Hash), action.Creator, uint32(action.Metadata.GetCascadeMetadata().RqMax))
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to generate RQID Files", fields)
		return nil, status.Errorf(codes.Internal, "failed to generate RQID Files")
	}

	<-task.NewAction(func(ctx context.Context) error {
		// sign the ticket if not primary node
		log.WithContext(ctx).Infof("isPrimary: %t", task.NetworkHandler.ConnectedTo == nil)
		if err = task.signAndSendCascadeTicket(ctx, task.NetworkHandler.ConnectedTo == nil, task.RQIDsFile); err != nil { // FIXME : use the right data
			log.WithContext(ctx).WithError(err).Errorf("signed and send Cascade ticket")
			err = errors.Errorf("signed and send Cascade ticket: %w", err)
			return nil
		}

		return nil
	})

	// only primary node start this action
	// var nftRegTxid string
	if task.NetworkHandler.ConnectedTo == nil {
		<-task.NewAction(func(ctx context.Context) error {
			log.WithContext(ctx).Debug("waiting for signature from peers")
			for {
				select {
				case <-ctx.Done():
					err = ctx.Err()
					if err != nil {
						log.WithContext(ctx).WithError(err).Error("waiting for signature from peers cancelled or timeout")
					}

					log.WithContext(ctx).Info("ctx done return from Validate & Register")
					return nil
				case <-task.AllSignaturesReceivedChn:
					log.WithContext(ctx).Info("all signature received so start validation")

					if err = task.lumeraClient.Verify(ctx, task.supernodeAddress, task.RQIDsFile, task.creatorSignature); err != nil { // FIXME : check signature
						log.WithContext(ctx).WithError(err).Errorf("peers' signature mismatched")
						err = errors.Errorf("peers' signature mismatched: %w", err)
						return nil
					}

					// TODO : MsgFinalizeAction

					return nil
				}
			}
		})
	}

	return &UploadInputDataResponse{
		Success: true,
		Message: "successfully uploaded input data",
	}, nil
}

// sign and send NFT ticket if not primary
func (task *CascadeRegistrationTask) signAndSendCascadeTicket(ctx context.Context, isPrimary bool, data []byte) error {
	signature, err := task.lumeraClient.Sign(ctx, task.supernodeAddress, data)
	if err != nil {
		return errors.Errorf("sign ticket: %w", err)
	}

	if !isPrimary {
		log.WithContext(ctx).Info("send signed cascade ticket to primary node")

		cascadeNode, ok := task.NetworkHandler.ConnectedTo.SuperNodePeerAPIInterface.(*CascadeRegistrationNode)
		if !ok {
			return errors.Errorf("node is not SenseRegistrationNode")
		}

		if err := cascadeNode.SendCascadeTicketSignature(ctx, task.config.PastelID, signature); err != nil {
			return errors.Errorf("send signature to primary node %s at address %s: %w", task.NetworkHandler.ConnectedTo.ID, task.NetworkHandler.ConnectedTo.Address, err)
		}
	}

	return nil
}
