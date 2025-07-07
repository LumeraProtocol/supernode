package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/supernode/services/common/supernode"
)

// StatusResponse represents the status response for cascade service
type StatusResponse = supernode.StatusResponse

// GetStatus delegates to the common supernode status service
func (service *CascadeService) GetStatus(ctx context.Context) (StatusResponse, error) {
	// Create a status service and register the cascade service as a task provider
	statusService := supernode.NewSupernodeStatusService()
	statusService.RegisterTaskProvider(service)

	// Get the status from the common service
	return statusService.GetStatus(ctx)
}

// GetStatus method for task interface compatibility
func (task *CascadeRegistrationTask) GetStatus(ctx context.Context) (StatusResponse, error) {
	return task.CascadeService.GetStatus(ctx)
}
