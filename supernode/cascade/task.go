package cascade

// CascadeRegistrationTask is the task for cascade registration
type CascadeRegistrationTask struct {
	*CascadeService

	taskID string
}

var _ CascadeTask = (*CascadeRegistrationTask)(nil)

// NewCascadeRegistrationTask returns a new Task instance
func NewCascadeRegistrationTask(service *CascadeService) *CascadeRegistrationTask {
	return &CascadeRegistrationTask{CascadeService: service}
}

// streamEvent sends a RegisterResponse via the provided callback.
func (task *CascadeRegistrationTask) streamEvent(eventType SupernodeEventType, msg, txHash string, send func(resp *RegisterResponse) error) {
	_ = send(&RegisterResponse{EventType: eventType, Message: msg, TxHash: txHash})
}
