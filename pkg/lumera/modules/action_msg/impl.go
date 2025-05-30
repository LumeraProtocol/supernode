package action_msg

import (
	"context"
	"fmt"

	actionapi "github.com/LumeraProtocol/lumera/api/lumera/action"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/auth"
	txmod "github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// module implements the Module interface using TxHelper for simplified transaction handling
type module struct {
	client   actiontypes.MsgClient
	txHelper *txmod.TxHelper
}

// newModule creates a new ActionMsg module client using TxHelper
func newModule(conn *grpc.ClientConn, authmodule auth.Module, txmodule txmod.Module, kr keyring.Keyring, keyName string, chainID string) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
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
	if authmodule == nil {
		return nil, fmt.Errorf("auth module cannot be nil")
	}
	if txmodule == nil {
		return nil, fmt.Errorf("tx module cannot be nil")
	}

	// Create TxHelper with default configuration
	txHelper := txmod.NewTxHelperWithDefaults(authmodule, txmodule, chainID, keyName, kr)

	return &module{
		client:   actiontypes.NewMsgClient(conn),
		txHelper: txHelper,
	}, nil
}

// FinalizeCascadeAction finalizes a CASCADE action with the given parameters
// This method is now much simpler thanks to TxHelper
func (m *module) FinalizeCascadeAction(ctx context.Context, actionId string, rqIdsIds []string) (*sdktx.BroadcastTxResponse, error) {
	// Step 1: Validate input parameters
	if actionId == "" {
		return nil, fmt.Errorf("action ID cannot be empty")
	}
	if len(rqIdsIds) == 0 {
		return nil, fmt.Errorf("rq_ids_ids cannot be empty for cascade action")
	}

	// Step 2: Use TxHelper to execute the transaction
	// The helper handles all the complexity of getting account info, signing, and broadcasting
	txResp, err := m.txHelper.ExecuteTransaction(ctx, func(creator string) (types.Msg, error) {
		return m.createFinalizeActionMessage(actionId, rqIdsIds, creator)
	})

	if err != nil {
		return nil, fmt.Errorf("failed to finalize cascade action: %w", err)
	}

	return txResp, nil

}

// createFinalizeActionMessage creates a MsgFinalizeAction message
// This is the core business logic of this module - creating the specific message type
func (m *module) createFinalizeActionMessage(actionId string, rqIdsIds []string, creator string) (*actiontypes.MsgFinalizeAction, error) {
	// Create CASCADE metadata
	cascadeMeta := actionapi.CascadeMetadata{
		RqIdsIds: rqIdsIds,
	}

	// Convert metadata to JSON
	metadataBytes, err := protojson.Marshal(&cascadeMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata to JSON: %w", err)
	}

	// Create and return the message
	msg := &actiontypes.MsgFinalizeAction{
		Creator:    creator,
		ActionId:   actionId,
		ActionType: "CASCADE",
		Metadata:   string(metadataBytes),
	}

	logtrace.Info(context.Background(), "created finalize action message", logtrace.Fields{
		"creator":    creator,
		"actionId":   actionId,
		"actionType": "CASCADE",
	})

	return msg, nil
}

// SetTxHelperConfig allows updating transaction configuration
func (m *module) SetTxHelperConfig(config *txmod.TxHelperConfig) {
	m.txHelper.UpdateConfig(config)
}

// GetTxHelper returns the underlying TxHelper for advanced usage
func (m *module) GetTxHelper() *txmod.TxHelper {
	return m.txHelper
}
