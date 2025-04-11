package node

import (
	"context"
	"fmt"

	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	signingtypes "github.com/cosmos/cosmos-sdk/types/tx/signing"

	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	kr     keyring.Keyring
	client cmtservice.ServiceClient
}

// newModule creates a new Node module client
func newModule(conn *grpc.ClientConn, keyring keyring.Keyring) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: cmtservice.NewServiceClient(conn),
		kr:     keyring,
	}, nil
}

// GetLatestBlock gets the latest block information
func (m *module) GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error) {
	resp, err := m.client.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block: %w", err)
	}

	return resp, nil
}

// GetBlockByHeight gets block information at a specific height
func (m *module) GetBlockByHeight(ctx context.Context, height int64) (*cmtservice.GetBlockByHeightResponse, error) {
	resp, err := m.client.GetBlockByHeight(ctx, &cmtservice.GetBlockByHeightRequest{
		Height: height,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get block at height %d: %w", height, err)
	}

	return resp, nil
}

// GetNodeInfo gets information about the node
func (m *module) GetNodeInfo(ctx context.Context) (*cmtservice.GetNodeInfoResponse, error) {
	resp, err := m.client.GetNodeInfo(ctx, &cmtservice.GetNodeInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node info: %w", err)
	}

	return resp, nil
}

// GetSyncing returns syncing state of the node
func (m *module) GetSyncing(ctx context.Context) (*cmtservice.GetSyncingResponse, error) {
	resp, err := m.client.GetSyncing(ctx, &cmtservice.GetSyncingRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get syncing status: %w", err)
	}

	return resp, nil
}

// GetLatestValidatorSet gets the latest validator set
func (m *module) GetLatestValidatorSet(ctx context.Context) (*cmtservice.GetLatestValidatorSetResponse, error) {
	resp, err := m.client.GetLatestValidatorSet(ctx, &cmtservice.GetLatestValidatorSetRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get latest validator set: %w", err)
	}

	return resp, nil
}

// GetValidatorSetByHeight gets the validator set at a specific height
func (m *module) GetValidatorSetByHeight(ctx context.Context, height int64) (*cmtservice.GetValidatorSetByHeightResponse, error) {
	resp, err := m.client.GetValidatorSetByHeight(ctx, &cmtservice.GetValidatorSetByHeightRequest{
		Height: height,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get validator set at height %d: %w", height, err)
	}

	return resp, nil
}

func (m *module) Sign(snAccAddress string, data []byte) (signature []byte, err error) {
	accAddr, err := types.AccAddressFromBech32(snAccAddress)
	if err != nil {
		return signature, fmt.Errorf("invalid address: %w", err)
	}

	_, err = m.kr.KeyByAddress(accAddr)
	if err != nil {
		return signature, fmt.Errorf("address not found in keyring: %w", err)
	}

	signature, _, err = m.kr.SignByAddress(accAddr, data, signingtypes.SignMode_SIGN_MODE_DIRECT)
	if err != nil {
		return nil, fmt.Errorf("failed to sign data: %w", err)
	}

	return signature, nil
}
