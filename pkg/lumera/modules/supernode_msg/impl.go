package supernode_msg

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	snquery "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

// module implements the Module interface for supernode-related transactions.
type module struct {
	client   sntypes.MsgClient
	query    snquery.Module
	txHelper *txmod.TxHelper
	mu       sync.Mutex
}

// newModule creates a new supernode_msg module instance.
func newModule(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
	supernodeQuery snquery.Module,
	kr keyring.Keyring,
	keyName string,
	chainID string,
) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}
	if authmodule == nil {
		return nil, fmt.Errorf("auth module cannot be nil")
	}
	if txmodule == nil {
		return nil, fmt.Errorf("tx module cannot be nil")
	}
	if supernodeQuery == nil {
		return nil, fmt.Errorf("supernode query module cannot be nil")
	}
	if kr == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if strings.TrimSpace(keyName) == "" {
		return nil, fmt.Errorf("key name cannot be empty")
	}
	if strings.TrimSpace(chainID) == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}

	return &module{
		client:   sntypes.NewMsgClient(conn),
		query:    supernodeQuery,
		txHelper: txmod.NewTxHelperWithDefaults(authmodule, txmodule, chainID, keyName, kr),
	}, nil
}

// ReportMetrics resolves on-chain supernode info and submits a
// MsgReportSupernodeMetrics transaction containing the provided metrics.
func (m *module) ReportMetrics(ctx context.Context, identity string, metrics sntypes.SupernodeMetrics) (*sdktx.BroadcastTxResponse, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("identity is empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Resolve validator and supernode account using the query module.
	snInfo, err := m.query.GetSupernodeWithLatestAddress(ctx, identity)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve supernode info: %w", err)
	}
	if snInfo == nil || snInfo.ValidatorAddress == "" || snInfo.SupernodeAccount == "" {
		return nil, fmt.Errorf("incomplete supernode info for identity %s", identity)
	}

	return m.txHelper.ExecuteTransaction(ctx, func(_ string) (sdktypes.Msg, error) {
		return &sntypes.MsgReportSupernodeMetrics{
			ValidatorAddress: snInfo.ValidatorAddress,
			SupernodeAccount: snInfo.SupernodeAccount,
			Metrics:          metrics,
		}, nil
	})
}
