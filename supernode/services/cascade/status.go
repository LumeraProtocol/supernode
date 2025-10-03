package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/supernode"
)

// StatusResponse represents the status response for cascade service
type StatusResponse = supernode.StatusResponse

// GetStatus delegates to the common supernode status service
func (service *CascadeService) GetStatus(ctx context.Context) (StatusResponse, error) {
	// Create a status service
	// Pass nil for optional dependencies (P2P, lumera client, and config)
	// as cascade service doesn't have access to them in this context
	statusService := supernode.NewSupernodeStatusService(nil, nil, nil)
	return statusService.GetStatus(ctx, false)
}
