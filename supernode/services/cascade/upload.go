package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/supernode/services/common"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UploadInputDataRequest struct {
	ActionID   string
	Filename   string
	DataHash   string
	RqMax      int32
	SignedData string
	Data       []byte
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

	res, err := task.raptorQ.GenRQIdentifiersFiles(ctx, raptorq.GenRQIdentifiersFilesRequest{
		TaskID:           task.ID(),
		BlockHash:        string(latestBlockHash),
		Data:             req.Data,
		CreatorSNAddress: actionDetails.GetCreator(),
		RqMax:            uint32(actionDetails.Metadata.GetCascadeMetadata().RqMax),
		SignedData:       req.SignedData,
		LC:               task.lumeraClient,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to generate RQID Files", fields)
		return nil, status.Errorf(codes.Internal, "failed to generate RQID Files")
	}
	logtrace.Info(ctx, "rq symbols, rq-ids and rqid-files have been generated", fields)

	task.RQInfo.rqIDsIC = res.RQIDsIc
	task.RQInfo.rqIDs = res.RQIDs
	task.RQInfo.rqIDFiles = res.RQIDsFiles
	task.RQInfo.rqIDsFile = res.RQIDsFile
	task.RQInfo.rqIDEncodeParams = res.RQEncodeParams
	task.creatorSignature = res.CreatorSignature

	// TODO : MsgFinalizeAction

	if err = task.storeIDFiles(ctx); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "error storing id files to p2p", fields)
		return nil, status.Errorf(codes.Internal, "error storing id files to p2p")
	}
	logtrace.Info(ctx, "id files have been stored", fields)

	if err = task.storeRaptorQSymbols(ctx); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "error storing raptor-q symbols", fields)
		return nil, status.Errorf(codes.Internal, "error storing raptor-q symbols")
	}
	logtrace.Info(ctx, "raptor-q symbols have been stored", fields)

	return &UploadInputDataResponse{
		Success: true,
		Message: "successfully uploaded input data",
	}, nil
}

func (task *CascadeRegistrationTask) storeIDFiles(ctx context.Context) error {
	ctx = context.WithValue(ctx, log.TaskIDKey, task.ID())
	task.storage.TaskID = task.ID()
	if err := task.storage.StoreBatch(ctx, task.RQInfo.rqIDFiles, common.P2PDataCascadeMetadata); err != nil {
		return errors.Errorf("store ID files into kademlia: %w", err)
	}
	return nil
}

func (task *CascadeRegistrationTask) storeRaptorQSymbols(ctx context.Context) error {
	return task.storage.StoreRaptorQSymbolsIntoP2P(ctx, task.ID())
}

//// validates RQIDs file
//func (task *CascadeRegistrationTask) validateRqIDs(ctx context.Context, dd []byte, ticket *ct.CascadeTicket) error {
//	snAccAddresses := []string{ticket.Creator}
//
//	var err error
//	task.rawRqFile, task.rqIDFiles, err = task.ValidateIDFiles(ctx, dd,
//		ticket.RQIDsIC, uint32(ticket.RQIDsMax),
//		ticket.RQIDs, 1,
//		snAccAddresses,
//		task.lumeraClient,
//		ticket.CreatorSignature,
//	)
//	if err != nil {
//		return errors.Errorf("validate rq_ids file: %w", err)
//	}
//
//	return nil
//}
//
//// validates actual RQ Symbol IDs inside RQIDs file
//func (task *CascadeRegistrationTask) validateRQSymbolID(ctx context.Context, ticket *ct.CascadeTicket) error {
//
//	content, err := task.Asset.Bytes()
//	if err != nil {
//		return errors.Errorf("read image contents: %w", err)
//	}
//
//	return task.storage.ValidateRaptorQSymbolIDs(ctx,
//		content /*uint32(len(task.Ticket.AppTicketData.RQIDs))*/, 1,
//		hex.EncodeToString([]byte(ticket.BlockHash)), ticket.Creator,
//		task.rawRqFile)
//}
