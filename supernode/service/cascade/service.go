package cascade

import (
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/lumera/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/supernode"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
)

type CascadeService struct {
	supernodeAddress string
	lumeraClient     *lumera.Client
	supernodeClient  *supernode.Client
	actionClient     *action.Client
	rqClient         *raptorq.Client
	rqConfig         *raptorq.Config
}

// NewCascadeService returns a new CascadeService instance.
func NewCascadeService(supernodeAddress string, lumeraC *lumera.Client, actionC *action.Client, supernodeC *supernode.Client, rqC *raptorq.Client, rqConf *raptorq.Config) *CascadeService {
	return &CascadeService{
		supernodeAddress: supernodeAddress,
		lumeraClient:     lumeraC,
		actionClient:     actionC,
		supernodeClient:  supernodeC,
		rqClient:         rqC,
		rqConfig:         rqConf,
	}
}

func (s *CascadeService) GetSNAddress() string {
	return s.supernodeAddress
}
