package cascadekit

import (
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
)

// NewCascadeMetadata creates a types.CascadeMetadata for RequestAction.
// The keeper will populate rq_ids_max; rq_ids_ids is for FinalizeAction only.
// commitment may be nil for pre-LEP-5 actions.
func NewCascadeMetadata(dataHashB64, fileName string, rqIdsIc uint64, indexSignatureFormat string, public bool, commitment *actiontypes.AvailabilityCommitment) actiontypes.CascadeMetadata {
	meta := actiontypes.CascadeMetadata{
		DataHash:   dataHashB64,
		FileName:   fileName,
		RqIdsIc:    rqIdsIc,
		Signatures: indexSignatureFormat,
		Public:     public,
	}
	if commitment != nil {
		meta.AvailabilityCommitment = commitment
	}
	return meta
}
