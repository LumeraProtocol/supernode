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
	// Pass nil for optional dependencies (P2P, lumera client, and config)
	// as cascade service doesn't have access to them in this context
	statusService := supernode.NewSupernodeStatusService(nil, nil, nil)
	statusService.RegisterTaskProvider(service)

	// Get the status from the common service
	return statusService.GetStatus(ctx)
}
