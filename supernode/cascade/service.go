package cascade

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/v2/supernode/adaptors"
)

type CascadeService struct {
	config *Config

	LumeraClient adaptors.LumeraClient
	P2P          adaptors.P2PService
	RQ           adaptors.CodecService
	P2PClient    p2p.Client
}

// Compile-time checks to ensure CascadeService implements required interfaces
var _ CascadeServiceFactory = (*CascadeService)(nil)

// NewCascadeRegistrationTask creates a new task for cascade registration
func (service *CascadeService) NewCascadeRegistrationTask() CascadeTask {
	task := NewCascadeRegistrationTask(service)
	return task
}

// Run starts the service (no background workers)
func (service *CascadeService) Run(ctx context.Context) error { <-ctx.Done(); return nil }

// NewCascadeService returns a new CascadeService instance
func NewCascadeService(config *Config, lumera lumera.Client, p2pClient p2p.Client, codec codec.Codec, rqstore rqstore.Store) *CascadeService {
	return &CascadeService{
		config:       config,
		LumeraClient: adaptors.NewLumeraClient(lumera),
		P2P:          adaptors.NewP2PService(p2pClient, rqstore),
		RQ:           adaptors.NewCodecService(codec),
		P2PClient:    p2pClient,
	}
}
