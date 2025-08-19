package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/storage/files"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/base"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/storage"
)

// CascadeRegistrationTask is the task for cascade registration
type CascadeRegistrationTask struct {
	*CascadeService

	*base.SuperNodeTask
	storage *storage.StorageHandler

	Asset            *files.File
	dataHash         string
	creatorSignature []byte
}

const (
	logPrefix = "cascade"
)

// Compile-time check to ensure CascadeRegistrationTask implements CascadeTask interface
var _ CascadeTask = (*CascadeRegistrationTask)(nil)

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
		SuperNodeTask:  base.NewSuperNodeTask(logPrefix),
		CascadeService: service,
	}

	return task
}

func (task *CascadeRegistrationTask) streamEvent(eventType SupernodeEventType, msg, txHash string, send func(resp *RegisterResponse) error) {
	_ = send(&RegisterResponse{
		EventType: eventType,
		Message:   msg,
		TxHash:    txHash,
	})

	return
}
