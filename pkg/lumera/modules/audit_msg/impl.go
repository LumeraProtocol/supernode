package audit_msg

import (
	"context"
	"fmt"
	"sync"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

type module struct {
	client   audittypes.MsgClient
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
		client:   audittypes.NewMsgClient(conn),
		txHelper: txmod.NewTxHelperWithDefaults(authmodule, txmodule, chainID, keyName, kr),
	}, nil
}

func (m *module) SubmitAuditReport(ctx context.Context, windowID uint64, selfReport audittypes.AuditSelfReport, peerObservations []*audittypes.AuditPeerObservation) (*sdktx.BroadcastTxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitAuditReport{
			SupernodeAccount: creator,
			WindowId:         windowID,
			SelfReport:       selfReport,
			PeerObservations: peerObservations,
		}, nil
	})
}
