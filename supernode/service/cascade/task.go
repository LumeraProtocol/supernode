package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/storage/files"
	"github.com/LumeraProtocol/supernode/pkg/types"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/LumeraProtocol/supernode/supernode/service/common"
)

// CascadeRegistrationTask is the task of registering new Sense.
type CascadeRegistrationTask struct {
	*common.SuperNodeTask
	*common.RegTaskHelper
	*CascadeService

	storage *common.StorageHandler

	Asset          *files.File // TODO : remove
	assetSizeBytes int
	dataHash       []byte

	Oti []byte

	creatorSignature []byte
	registrationFee  int64

	RQIDsIc   uint32
	RQIDs     []string
	RQIDsFile []byte

	rawRqFile []byte
	rqIDFiles [][]byte
}

const (
	logPrefix = "cascade"
)

// Run starts the task
func (task *CascadeRegistrationTask) Run(ctx context.Context) error {
	return task.RunHelper(ctx, task.removeArtifacts)
}

// SendRegMetadata receives registration metadata -
//
//	caller/creator PastelID; block when ticket registration has started; txid of the pre-burn fee
func (task *CascadeRegistrationTask) SendRegMetadata(_ context.Context, regMetadata *types.ActionRegMetadata) error {
	if err := task.RequiredStatus(common.StatusConnected); err != nil {
		return err
	}
	task.ActionTicketRegMetadata = regMetadata

	return nil
}

// UploadAsset uploads the asset
func (task *CascadeRegistrationTask) UploadAsset(ctx context.Context, file *files.File) error {
	if err := task.RequiredStatus(common.StatusConnected); err != nil {
		return errors.Errorf("require status %s not satisfied", common.StatusConnected)
	}

	task.UpdateStatus(common.StatusAssetUploaded)

	task.Asset = file
	fileBytes, err := file.Bytes()
	if err != nil {
		log.WithContext(ctx).WithError(err).Errorf("read image file")
		return errors.Errorf("read image file: %w", err)
	}
	task.assetSizeBytes = len(fileBytes)

	fileDataInMb := utils.GetFileSizeInMB(fileBytes)
	fee, err := task.lumeraClient.GetEstimatedCascadeFee(ctx, fileDataInMb)
	if err != nil {
		return errors.Errorf("getting estimated fee %w", err)
	}

	task.registrationFee = int64(fee)
	task.ActionTicketRegMetadata.EstimatedFee = task.registrationFee
	task.RegTaskHelper.ActionTicketRegMetadata.EstimatedFee = task.registrationFee

	return nil
}

func (task *CascadeRegistrationTask) removeArtifacts() {
	task.RemoveFile(task.Asset)
}

// NewCascadeRegistrationTask returns a new Task instance.
func NewCascadeRegistrationTask(service *CascadeService) *CascadeRegistrationTask {

	task := &CascadeRegistrationTask{
		SuperNodeTask:  common.NewSuperNodeTask(logPrefix, service.historyDB),
		CascadeService: service,
		storage: common.NewStorageHandler(service.P2PClient, service.rqClient,
			service.config.RaptorQServiceAddress, service.config.RqFilesDir, service.rqstore),
	}

	task.RegTaskHelper = common.NewRegTaskHelper(task.SuperNodeTask,
		task.config.PastelID, task.config.PassPhrase,
		common.NewNetworkHandler(task.SuperNodeTask, service.nodeClient,
			RegisterCascadeNodeMaker{}, *service.lumeraClient, task.config.PastelID,
			service.config.NumberConnectedNodes),
		*service.lumeraClient, task.config.PreburntTxMinConfirmations)

	return task
}
