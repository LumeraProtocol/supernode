package audit_msg

import (
	"context"
	"fmt"
	"strings"
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

func newModule(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
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
		client:   audittypes.NewMsgClient(conn),
		txHelper: txmod.NewTxHelperWithDefaults(authmodule, txmodule, chainID, keyName, kr),
	}, nil
}

func (m *module) SubmitEvidence(ctx context.Context, subjectAddress string, evidenceType audittypes.EvidenceType, actionID string, metadataJSON string) (*sdktx.BroadcastTxResponse, error) {
	subjectAddress = strings.TrimSpace(subjectAddress)
	if subjectAddress == "" {
		return nil, fmt.Errorf("subject address cannot be empty")
	}
	metadataJSON = strings.TrimSpace(metadataJSON)
	if metadataJSON == "" {
		return nil, fmt.Errorf("metadata cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitEvidence{
			Creator:        creator,
			SubjectAddress: subjectAddress,
			EvidenceType:   evidenceType,
			ActionId:       actionID,
			Metadata:       metadataJSON,
		}, nil
	})
}

func (m *module) SubmitEpochReport(ctx context.Context, epochID uint64, hostReport audittypes.HostReport, storageChallengeObservations []*audittypes.StorageChallengeObservation) (*sdktx.BroadcastTxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Intentionally submit 0% usage for CPU/memory so the chain treats these as "unknown"
	// (see x/audit enforcement semantics).
	//
	// Disk usage is expected to be reported accurately (legacy-aligned); callers provide it.
	hostReport.CpuUsagePercent = 0
	hostReport.MemUsagePercent = 0

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitEpochReport{
			Creator:                      creator,
			EpochId:                      epochID,
			HostReport:                   hostReport,
			StorageChallengeObservations: storageChallengeObservations,
		}, nil
	})
}
