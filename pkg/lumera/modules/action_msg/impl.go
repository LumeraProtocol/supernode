package action_msg

import (
	"context"
	"fmt"
	"sync"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

type module struct {
	client   actiontypes.MsgClient
	txHelper *txmod.TxHelper
	mu       sync.Mutex
}

func newModule(conn *grpc.ClientConn, authmodule auth.Module, txmodule txmod.Module, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}
	if authmodule == nil {
		return nil, fmt.Errorf("auth module cannot be nil")
	}
	if txmodule == nil {
		return nil, fmt.Errorf("tx module cannot be nil")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if keyName == "" {
		return nil, fmt.Errorf("key name cannot be empty")
	}
	if chainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}

	return &module{
		client:   actiontypes.NewMsgClient(conn),
		txHelper: txmod.NewTxHelperWithDefaults(authmodule, txmodule, chainID, keyName, kr),
	}, nil
}

func (m *module) RequestAction(ctx context.Context, actionType, metadata, price, expirationTime string) (*sdktx.BroadcastTxResponse, error) {
	if err := validateRequestActionParams(actionType, metadata, price, expirationTime); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (types.Msg, error) {
		return createRequestActionMessage(creator, actionType, metadata, price, expirationTime), nil
	})
}

func (m *module) FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*sdktx.BroadcastTxResponse, error) {
	if err := validateFinalizeActionParams(actionId, rqIdsIds); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (types.Msg, error) {
		return createFinalizeActionMessage(creator, actionId, rqIdsIds)
	})
}

func (m *module) SetTxHelperConfig(config *txmod.TxHelperConfig) {
	if config == nil {
		return
	}
	m.txHelper.UpdateConfig(config)
}

func (m *module) GetTxHelper() *txmod.TxHelper {
	return m.txHelper
}

// SimulateFinalizeCascadeAction builds the finalize message and performs a simulation
// without broadcasting the transaction. This is useful to ensure the transaction
// would pass ante/ValidateBasic before doing irreversible work.
func (m *module) SimulateFinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*sdktx.SimulateResponse, error) {
	if err := validateFinalizeActionParams(actionId, rqIdsIds); err != nil {
		return nil, err
	}

	// Gather account info and creator address
	accountInfo, err := m.txHelper.GetAccountInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}
	creator, err := m.txHelper.GetCreatorAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get creator address: %w", err)
	}

	// Build the finalize message
	msg, err := createFinalizeActionMessage(creator, actionId, rqIdsIds)
	if err != nil {
		return nil, err
	}

	// Run simulation using tx helper
	return m.txHelper.Simulate(ctx, []types.Msg{msg}, accountInfo)
}
