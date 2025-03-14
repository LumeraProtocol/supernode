package node

import (
	"context"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"
)

// Module defines the interface for interacting with node status information
type Module interface {
	// GetLatestBlock gets the latest block information
	GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error)

	// GetBlockByHeight gets block information at a specific height
	GetBlockByHeight(ctx context.Context, height int64) (*cmtservice.GetBlockByHeightResponse, error)

	// GetNodeInfo gets information about the node
	GetNodeInfo(ctx context.Context) (*cmtservice.GetNodeInfoResponse, error)

	// GetSyncing returns syncing state of the node
	GetSyncing(ctx context.Context) (*cmtservice.GetSyncingResponse, error)

	// GetLatestValidatorSet gets the latest validator set
	GetLatestValidatorSet(ctx context.Context) (*cmtservice.GetLatestValidatorSetResponse, error)

	// GetValidatorSetByHeight gets the validator set at a specific height
	GetValidatorSetByHeight(ctx context.Context, height int64) (*cmtservice.GetValidatorSetByHeightResponse, error)

	// Sign signs the given bytes with the supernodeAccountAddress and returns the signature
	Sign(snAccAddress string, data []byte) (signature []byte, err error)

	// Verify verifies the given bytes with given supernodeAccAddress public key and returns the error
	Verify(accAddress string, data, signature []byte) (err error)
}

// NewModule creates a new Node module client
func NewModule(conn *grpc.ClientConn, kr keyring.Keyring) (Module, error) {
	return newModule(conn, kr)
}
