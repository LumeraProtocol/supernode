package cascade

import (
	"context"
	"encoding/hex"
	"encoding/json"

	ct "github.com/LumeraProtocol/supernode/pkg/common/task"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"

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
	task.Creator = action.Creator

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
	task.rqIDsIC, task.rqIDs, task.rqIDsFile, task.rqIDEncodeParams, task.creatorSignature, err = task.rqClient.GenRQIdentifiersFiles(ctx, fields, nil, string(latestBlock.Hash), action.Creator, uint32(action.Metadata.GetCascadeMetadata().RqMax))
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to generate RQID Files", fields)
		return nil, status.Errorf(codes.Internal, "failed to generate RQID Files")
	}

	cascadeTicket := ct.CascadeTicket{
		Creator:          action.Creator,
		CreatorSignature: task.creatorSignature,
		ActionID:         action.ActionID,
		DataHash:         action.Metadata.GetCascadeMetadata().DataHash,
		BlockHeight:      latestBlock.Height,
		BlockHash:        latestBlock.Hash, // FIXME : verify, to be used in validateRQSymbolID
		RQIDsIC:          task.rqIDsIC,
		RQIDs:            task.rqIDs,
		RQIDsMax:         action.Metadata.CascadeMetadata.RqMax,
	}
	ticket, err := json.Marshal(cascadeTicket)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed marshall the cascade ticket", fields)
		return nil, status.Errorf(codes.Internal, "failed marshall the cascade ticket")
	}

	<-task.NewAction(func(ctx context.Context) error {
		// sign the ticket if not primary node
		log.WithContext(ctx).Infof("isPrimary: %t", task.NetworkHandler.ConnectedTo == nil)
		if err = task.signAndSendCascadeTicket(ctx, task.NetworkHandler.ConnectedTo == nil, ticket, task.rqIDsFile, task.rqIDEncodeParams); err != nil { // FIXME : use the right data
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
func (task *CascadeRegistrationTask) signAndSendCascadeTicket(ctx context.Context, isPrimary bool, ticket []byte, data []byte, rqEncodeParams raptorq.EncoderParameters) (err error) {
	task.creatorSignature, err = task.lumeraClient.Sign(ctx, task.config.SupernodeAccountAddress, ticket)
	if err != nil {
		return errors.Errorf("sign ticket: %w", err)
	}

	if !isPrimary {
		log.WithContext(ctx).Info("send signed cascade ticket to primary node")

		cascadeNode, ok := task.NetworkHandler.ConnectedTo.SuperNodePeerAPIInterface.(*CascadeRegistrationNode)
		if !ok {
			return errors.Errorf("node is not SenseRegistrationNode")
		}

		if err := cascadeNode.SendCascadeTicketSignature(ctx, task.config.SupernodeAccountAddress, task.creatorSignature, data, task.rqIDsFile, rqEncodeParams); err != nil { // FIXME : nodeID
			return errors.Errorf("send signature to primary node %s at address %s: %w", task.NetworkHandler.ConnectedTo.ID, task.NetworkHandler.ConnectedTo.Address, err)
		}
	}

	return nil
}

func (task *CascadeRegistrationTask) ValidateSignedTicketFromSecondaryNode(ctx context.Context,
	ticket []byte, creatorSignature []byte, rqidFile []byte) error {
	var err error
	// task.creatorSignature = creatorSignature

	var cascadeTask ct.CascadeTicket
	err = json.Unmarshal(ticket, &cascadeTask)
	if err != nil {
		log.WithContext(ctx).WithError(err).Errorf("unmarshal cascade ticket signature")
		return errors.Errorf("unmarshal cascade ticket signature %w", err)
	}

	err = task.lumeraClient.Verify(ctx, task.config.SupernodeAccountAddress, ticket, creatorSignature)
	if err != nil {
		log.WithContext(ctx).WithError(err).Errorf("verify cascade ticket signature")
		return errors.Errorf("verify cascade ticket signature %w", err)
	}

	if err := task.validateRqIDs(ctx, rqidFile, &cascadeTask); err != nil {
		log.WithContext(ctx).WithError(err).Errorf("validate rqids files")

		return errors.Errorf("validate rq & dd id files %w", err)
	}

	if err = task.validateRQSymbolID(ctx, &cascadeTask); err != nil {
		log.WithContext(ctx).WithError(err).Errorf("valdate rq ids inside rqids file")
		err = errors.Errorf("generate rqids: %w", err)
		return nil
	}

	task.dataHash = cascadeTask.DataHash

	return nil
}

// validates RQIDs file
func (task *CascadeRegistrationTask) validateRqIDs(ctx context.Context, dd []byte, ticket *ct.CascadeTicket) error {
	pastelIDs := []string{ticket.Creator}

	var err error
	task.rawRqFile, task.rqIDFiles, err = task.ValidateIDFiles(ctx, dd,
		ticket.RQIDsIC, uint32(ticket.RQIDsMax),
		ticket.RQIDs, 1,
		pastelIDs,
		*task.lumeraClient,
		ticket.CreatorSignature,
	)
	if err != nil {
		return errors.Errorf("validate rq_ids file: %w", err)
	}

	return nil
}

// validates actual RQ Symbol IDs inside RQIDs file
func (task *CascadeRegistrationTask) validateRQSymbolID(ctx context.Context, ticket *ct.CascadeTicket) error {

	content, err := task.Asset.Bytes()
	if err != nil {
		return errors.Errorf("read image contents: %w", err)
	}

	return task.storage.ValidateRaptorQSymbolIDs(ctx,
		content /*uint32(len(task.Ticket.AppTicketData.RQIDs))*/, 1,
		hex.EncodeToString([]byte(ticket.BlockHash)), ticket.Creator,
		task.rawRqFile)
}
