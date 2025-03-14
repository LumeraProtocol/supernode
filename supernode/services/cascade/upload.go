package cascade

import (
	"context"
	"encoding/hex"
	"encoding/json"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/types"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"

	ct "github.com/LumeraProtocol/supernode/pkg/common/task"
	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
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

	actionRes, err := task.lumeraClient.Action().GetAction(ctx, req.ActionID)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get action", fields)
		return nil, status.Errorf(codes.Internal, "failed to get action")
	}
	if actionRes.GetAction().ActionID == "" {
		logtrace.Error(ctx, "action not found", fields)
		return nil, status.Errorf(codes.Internal, "action not found")
	}
	actionDetails := actionRes.GetAction()
	logtrace.Info(ctx, "action has been retrieved", fields)

	latestBlock, err := task.lumeraClient.Node().GetLatestBlock(ctx)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get latest block", fields)
		return nil, status.Errorf(codes.Internal, "failed to get latest block")
	}
	latestBlockHeight := uint64(latestBlock.GetSdkBlock().GetHeader().Height)
	latestBlockHash := latestBlock.GetBlockId().GetHash()
	fields[logtrace.FieldBlockHeight] = latestBlockHeight
	logtrace.Info(ctx, "latest block has been retrieved", fields)

	topSNsRes, err := task.lumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, latestBlockHeight)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get top SNs", fields)
		return nil, status.Errorf(codes.Internal, "failed to get top SNs")
	}
	logtrace.Info(ctx, "top sns have been fetched", fields)

	if !supernode.Exists(topSNsRes.Supernodes, task.config.SupernodeAccountAddress) {
		logtrace.Error(ctx, "current supernode do not exist in the top sns list", fields)
		return nil, status.Errorf(codes.Internal, "current supernode does not exist in the top sns list")
	}
	logtrace.Info(ctx, "current supernode exists in the top sns list", fields)

	if req.DataHash != actionDetails.Metadata.GetCascadeMetadata().DataHash {
		logtrace.Error(ctx, "data hash doesn't match", fields)
		return nil, status.Errorf(codes.Internal, "data hash doesn't match")
	}
	logtrace.Info(ctx, "request data-hash has been matched with the action data-hash", fields)

	// FIXME : use proper file
	task.rqIDsIC, task.rqIDs,
		task.rqIDsFile, task.rqIDEncodeParams, task.creatorSignature, err = task.raptorQ.GenRQIdentifiersFiles(ctx,
		fields,
		nil,
		string(latestBlockHash), actionDetails.GetCreator(),
		uint32(actionDetails.Metadata.GetCascadeMetadata().RqMax),
	)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to generate RQID Files", fields)
		return nil, status.Errorf(codes.Internal, "failed to generate RQID Files")
	}
	logtrace.Info(ctx, "rq symbols have been generated", fields)

	ticket, err := task.createCascadeActionTicket(ctx, actionDetails, *latestBlock)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to create cascade ticket", fields)
		return nil, status.Errorf(codes.Internal, "failed to create cascade ticket")
	}
	logtrace.Info(ctx, "cascade ticket created", fields)

	switch task.NetworkHandler.IsPrimary() {
	case true:
		<-task.NewAction(func(ctx context.Context) error {
			logtrace.Info(ctx, "primary node flow, waiting for signature from peers", fields)
			for {
				select {
				case <-ctx.Done():
					err = ctx.Err()
					if err != nil {
						logtrace.Info(ctx, "waiting for signature from peers cancelled or timeout", fields)
					}

					logtrace.Info(ctx, "ctx done return from Validate & Register", fields)
					return nil
				case <-task.AllSignaturesReceivedChn:
					logtrace.Info(ctx, "all signature received so start validation", fields)

					// TODO : MsgFinalizeAction

					return nil
				}
			}
		})
	case false:
		<-task.NewAction(func(ctx context.Context) error {
			logtrace.Info(ctx, "secondary node flow, sending data with signature to primary node for validation", fields)

			if err = task.signAndSendCascadeTicket(ctx, task.NetworkHandler.ConnectedTo == nil, ticket, task.rqIDsFile, task.rqIDEncodeParams); err != nil { // FIXME : use the right data
				fields[logtrace.FieldError] = err.Error()
				logtrace.Error(ctx, "failed to sign & send cascade ticket to the primary node", fields)
				return status.Errorf(codes.Internal, "failed to sign and send cascade ticket")
			}

			return nil
		})
	}

	return &UploadInputDataResponse{
		Success: true,
		Message: "successfully uploaded input data",
	}, nil
}

// sign and send NFT ticket if not primary
func (task *CascadeRegistrationTask) signAndSendCascadeTicket(ctx context.Context, isPrimary bool, ticket []byte, data []byte, rqEncodeParams raptorq.EncoderParameters) (err error) {
	secondaryNodeSignature, err := task.lumeraClient.Node().Sign(task.config.SupernodeAccountAddress, ticket)
	if err != nil {
		return errors.Errorf("sign ticket: %w", err)
	}

	if !isPrimary {
		log.WithContext(ctx).Info("send signed cascade ticket to primary node")

		cascadeNode, ok := task.NetworkHandler.ConnectedTo.SuperNodePeerAPIInterface.(*CascadeRegistrationNode)
		if !ok {
			return errors.Errorf("node is not SenseRegistrationNode")
		}

		if err := cascadeNode.SendCascadeTicketSignature(ctx, task.config.SupernodeAccountAddress, secondaryNodeSignature, data, task.rqIDsFile, rqEncodeParams); err != nil { // FIXME : nodeID
			return errors.Errorf("send signature to primary node %s at address %s: %w", task.NetworkHandler.ConnectedTo.ID, task.NetworkHandler.ConnectedTo.Address, err)
		}
	}

	return nil
}

func (task *CascadeRegistrationTask) ValidateSignedTicketFromSecondaryNode(ctx context.Context,
	ticket []byte, supernodeAccAddress string, supernodeSignature []byte, rqidFile []byte) error {
	var err error

	fields := logtrace.Fields{
		logtrace.FieldMethod:                  "ValidateSignedTicketFromSecondaryNode",
		logtrace.FieldSupernodeAccountAddress: supernodeAccAddress,
	}
	logtrace.Info(ctx, "request has been received to validate signature", fields)

	err = task.lumeraClient.Node().Verify(supernodeAccAddress, ticket, supernodeSignature)
	if err != nil {
		log.WithContext(ctx).WithError(err).Errorf("error verifying the secondary-supernode signature")
		return errors.Errorf("verify cascade ticket signature %w", err)
	}
	logtrace.Info(ctx, "seconday-supernode signature has been verified", fields)

	var cascadeData ct.CascadeTicket
	err = json.Unmarshal(ticket, &cascadeData)
	if err != nil {
		log.WithContext(ctx).WithError(err).Errorf("unmarshal cascade ticket signature")
		return errors.Errorf("unmarshal cascade ticket signature %w", err)
	}
	logtrace.Info(ctx, "data has been unmarshalled", fields)

	if err := task.validateRqIDs(ctx, rqidFile, &cascadeData); err != nil {
		log.WithContext(ctx).WithError(err).Errorf("validate rqids files")

		return errors.Errorf("validate rq & dd id files %w", err)
	}

	if err = task.validateRQSymbolID(ctx, &cascadeData); err != nil {
		log.WithContext(ctx).WithError(err).Errorf("valdate rq ids inside rqids file")
		err = errors.Errorf("generate rqids: %w", err)
		return nil
	}

	task.dataHash = cascadeData.DataHash

	return nil
}

// validates RQIDs file
func (task *CascadeRegistrationTask) validateRqIDs(ctx context.Context, dd []byte, ticket *ct.CascadeTicket) error {
	snAccAddresses := []string{ticket.Creator}

	var err error
	task.rawRqFile, task.rqIDFiles, err = task.ValidateIDFiles(ctx, dd,
		ticket.RQIDsIC, uint32(ticket.RQIDsMax),
		ticket.RQIDs, 1,
		snAccAddresses,
		task.lumeraClient,
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

func (task *CascadeRegistrationTask) createCascadeActionTicket(ctx context.Context,
	actionDetails *actiontypes.Action, latestBlock cmtservice.GetLatestBlockResponse) ([]byte, error) {
	t := ct.CascadeTicket{
		ActionID:         actionDetails.ActionID,
		BlockHeight:      latestBlock.GetSdkBlock().GetHeader().Height,
		BlockHash:        latestBlock.GetBlockId().GetHash(),
		Creator:          actionDetails.GetCreator(),
		CreatorSignature: task.creatorSignature,
		DataHash:         actionDetails.Metadata.GetCascadeMetadata().DataHash,
		RQIDsIC:          task.rqIDsIC,
		RQIDs:            task.rqIDs,
		RQIDsMax:         actionDetails.GetMetadata().GetCascadeMetadata().RqMax,
	}
	ticket, err := json.Marshal(t)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed marshall the cascade ticket")
	}

	return ticket, nil
}
