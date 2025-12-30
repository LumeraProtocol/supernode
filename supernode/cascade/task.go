package cascade

import "context"

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
// It propagates send failures so callers can abort work when the downstream is gone.
func (task *CascadeRegistrationTask) streamEvent(ctx context.Context, eventType SupernodeEventType, msg, txHash string, send func(resp *RegisterResponse) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return send(&RegisterResponse{EventType: eventType, Message: msg, TxHash: txHash})
}
