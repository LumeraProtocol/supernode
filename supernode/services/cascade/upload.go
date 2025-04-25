package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/supernode/services/common"

	actiontypes "github.com/LumeraProtocol/supernode/gen/lumera/action/types"
	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UploadInputDataRequest contains parameters for upload request
type UploadInputDataRequest struct {
	ActionID   string
	Filename   string
	DataHash   string
	RqMax      int32
	SignedData string
	Data       []byte
}

// UploadInputDataResponse contains the result of upload
type UploadInputDataResponse struct {
	Success bool
	Message string
}

// UploadInputData processes the upload request for cascade input data
func (task *CascadeRegistrationTask) UploadInputData(ctx context.Context, req *UploadInputDataRequest) (*UploadInputDataResponse, error) {
	fields := logtrace.Fields{
		logtrace.FieldMethod:  "UploadInputData",
		logtrace.FieldRequest: req,
	}

	// Get action details from Lumera
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

	// Get latest block information
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

	// Get top supernodes
	topSNsRes, err := task.lumeraClient.SuperNode().GetTopSuperNodesForBlock(ctx, latestBlockHeight)
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to get top SNs", fields)
		return nil, status.Errorf(codes.Internal, "failed to get top SNs")
	}
	logtrace.Info(ctx, "top sns have been fetched", fields)

	// Verify current supernode is in the top list
	if !supernode.Exists(topSNsRes.Supernodes, task.config.SupernodeAccountAddress) {
		logtrace.Error(ctx, "current supernode do not exist in the top sns list", fields)
		return nil, status.Errorf(codes.Internal, "current supernode does not exist in the top sns list")
	}
	logtrace.Info(ctx, "current supernode exists in the top sns list", fields)

	// Parse the action metadata to CascadeMetadata
	var cascadeMetadata actiontypes.CascadeMetadata
	if err := proto.Unmarshal(actionDetails.Metadata, &cascadeMetadata); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to unmarshal cascade metadata", fields)
		return nil, status.Errorf(codes.Internal, "failed to unmarshal cascade metadata")
	}

	// Verify data hash matches action metadata
	if req.DataHash != cascadeMetadata.DataHash {
		logtrace.Error(ctx, "data hash doesn't match", fields)
		return nil, status.Errorf(codes.Internal, "data hash doesn't match")
	}
	logtrace.Info(ctx, "request data-hash has been matched with the action data-hash", fields)

	// Generate RaptorQ identifiers
	res, err := task.raptorQ.GenRQIdentifiersFiles(ctx, raptorq.GenRQIdentifiersFilesRequest{
		BlockHash:        string(latestBlockHash),
		Data:             req.Data,
		CreatorSNAddress: actionDetails.GetCreator(),
		RqMax:            uint32(cascadeMetadata.RqIdsMax),
		SignedData:       req.SignedData,
		LC:               task.lumeraClient,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to generate RQID Files", fields)
		return nil, status.Errorf(codes.Internal, "failed to generate RQID Files")
	}
	logtrace.Info(ctx, "rq symbols, rq-ids and rqid-files have been generated", fields)

	// Store RaptorQ information
	task.RQInfo.rqIDsIC = res.RQIDsIc
	task.RQInfo.rqIDs = res.RQIDs
	task.RQInfo.rqIDFiles = res.RQIDsFiles
	task.RQInfo.rqIDsFile = res.RQIDsFile
	task.RQInfo.rqIDEncodeParams = res.RQEncodeParams
	task.creatorSignature = res.CreatorSignature

	// Store ID files to P2P
	if err = task.storeIDFiles(ctx); err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "error storing id files to p2p", fields)
		return nil, status.Errorf(codes.Internal, "error storing id files to p2p")
	}
	logtrace.Info(ctx, "id files have been stored", fields)

	// Store RaptorQ symbols
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

// storeIDFiles stores ID files to P2P
func (task *CascadeRegistrationTask) storeIDFiles(ctx context.Context) error {
	ctx = context.WithValue(ctx, log.TaskIDKey, task.ID())
	task.storage.TaskID = task.ID()
	if err := task.storage.StoreBatch(ctx, task.RQInfo.rqIDFiles, common.P2PDataCascadeMetadata); err != nil {
		return errors.Errorf("store ID files into kademlia: %w", err)
	}
	return nil
}

// storeRaptorQSymbols stores RaptorQ symbols to P2P
func (task *CascadeRegistrationTask) storeRaptorQSymbols(ctx context.Context) error {
	return task.storage.StoreRaptorQSymbolsIntoP2P(ctx, task.ID())
}
