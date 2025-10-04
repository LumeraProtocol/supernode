package cascade

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/files"
)

// CascadeRegistrationTask is the task for cascade registration
type CascadeRegistrationTask struct {
	*CascadeService

	Asset            *files.File
	dataHash         string
	creatorSignature []byte
	taskID           string
}

const (
	logPrefix = "cascade"
)

// Compile-time check to ensure CascadeRegistrationTask implements CascadeTask interface
var _ CascadeTask = (*CascadeRegistrationTask)(nil)

func (task *CascadeRegistrationTask) removeArtifacts() {
	if task.Asset != nil {
		_ = task.Asset.Remove()
	}
}

// NewCascadeRegistrationTask returns a new Task instance
func NewCascadeRegistrationTask(service *CascadeService) *CascadeRegistrationTask {
	return &CascadeRegistrationTask{CascadeService: service}
}

// streamEvent sends a RegisterResponse via the provided callback.
func (task *CascadeRegistrationTask) streamEvent(eventType SupernodeEventType, msg, txHash string, send func(resp *RegisterResponse) error) {
	_ = send(&RegisterResponse{EventType: eventType, Message: msg, TxHash: txHash})
}
