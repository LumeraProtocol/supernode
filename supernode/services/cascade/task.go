package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/storage/files"
	"github.com/LumeraProtocol/supernode/supernode/services/common"
)

// RQInfo holds RaptorQ-related information
type RQInfo struct {
	rqIDsIC          uint32
	rqIDs            []string
	rqIDEncodeParams raptorq.EncoderParameters

	rqIDsFile []byte
	rawRqFile []byte
	rqIDFiles [][]byte
}

// CascadeRegistrationTask is the task for cascade registration
type CascadeRegistrationTask struct {
	RQInfo
	*CascadeService

	*common.SuperNodeTask
	storage *common.StorageHandler

	Asset            *files.File
	dataHash         string
	creatorSignature []byte
}

const (
	logPrefix = "cascade"
)

// Run starts the task
func (task *CascadeRegistrationTask) Run(ctx context.Context) error {
	return task.RunHelper(ctx, task.removeArtifacts)
}

// removeArtifacts cleans up any files created during processing
func (task *CascadeRegistrationTask) removeArtifacts() {
	task.RemoveFile(task.Asset)
}

// NewCascadeRegistrationTask returns a new Task instance
func NewCascadeRegistrationTask(service *CascadeService) *CascadeRegistrationTask {
	task := &CascadeRegistrationTask{
		SuperNodeTask:  common.NewSuperNodeTask(logPrefix),
		CascadeService: service,
		storage: common.NewStorageHandler(service.P2PClient, service.raptorQClient,
			service.config.RaptorQServiceAddress, service.config.RqFilesDir, service.rqstore),
	}

	return task
}
