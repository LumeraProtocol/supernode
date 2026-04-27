package audit_msg

import (
	"context"
	"fmt"
	"strings"
	"sync"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	txmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"google.golang.org/grpc"
)

type module struct {
	client   audittypes.MsgClient
	txHelper *txmod.TxHelper
	mu       sync.Mutex
}

// newModuleWithHelper creates the audit_msg module using the supplied
// TxHelperConfig (normalized by tx.NewTxHelper).
func newModuleWithHelper(
	conn *grpc.ClientConn,
	authmodule auth.Module,
	txmodule txmod.Module,
	cfg *txmod.TxHelperConfig,
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
	if cfg == nil {
		return nil, fmt.Errorf("tx helper config cannot be nil")
	}
	if cfg.Keyring == nil {
		return nil, fmt.Errorf("keyring cannot be nil")
	}
	if strings.TrimSpace(cfg.KeyName) == "" {
		return nil, fmt.Errorf("key name cannot be empty")
	}
	if strings.TrimSpace(cfg.ChainID) == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}

	return &module{
		client:   audittypes.NewMsgClient(conn),
		txHelper: txmod.NewTxHelper(authmodule, txmodule, cfg),
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

func (m *module) SubmitEpochReport(
	ctx context.Context,
	epochID uint64,
	hostReport audittypes.HostReport,
	storageChallengeObservations []*audittypes.StorageChallengeObservation,
	storageProofResults []*audittypes.StorageProofResult,
) (*sdktx.BroadcastTxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Zero CpuUsagePercent and MemUsagePercent: deliberate workaround for a
	// pre-LEP-6 deterministic-state issue where non-deterministic CPU/mem
	// samples could diverge across validators. Submitting 0% causes the chain
	// to treat these as "unknown" (see x/audit enforcement semantics).
	// Disk usage is expected to be reported accurately (legacy-aligned).
	// Per LEP-6 Plan v2 PR1 fixups — DO NOT REMOVE without chain-team review.
	hostReport.CpuUsagePercent = 0
	hostReport.MemUsagePercent = 0

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitEpochReport{
			Creator:                      creator,
			EpochId:                      epochID,
			HostReport:                   hostReport,
			StorageChallengeObservations: storageChallengeObservations,
			StorageProofResults:          storageProofResults,
		}, nil
	})
}

func (m *module) SubmitStorageRecheckEvidence(
	ctx context.Context,
	epochID uint64,
	challengedSupernodeAccount string,
	ticketID string,
	challengedResultTranscriptHash string,
	recheckTranscriptHash string,
	recheckResultClass audittypes.StorageProofResultClass,
	details string,
) (*sdktx.BroadcastTxResponse, error) {
	challengedSupernodeAccount = strings.TrimSpace(challengedSupernodeAccount)
	if challengedSupernodeAccount == "" {
		return nil, fmt.Errorf("challenged supernode account cannot be empty")
	}
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return nil, fmt.Errorf("ticket id cannot be empty")
	}
	challengedResultTranscriptHash = strings.TrimSpace(challengedResultTranscriptHash)
	if challengedResultTranscriptHash == "" {
		return nil, fmt.Errorf("challenged result transcript hash cannot be empty")
	}
	recheckTranscriptHash = strings.TrimSpace(recheckTranscriptHash)
	if recheckTranscriptHash == "" {
		return nil, fmt.Errorf("recheck transcript hash cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitStorageRecheckEvidence{
			Creator:                        creator,
			EpochId:                        epochID,
			ChallengedSupernodeAccount:     challengedSupernodeAccount,
			TicketId:                       ticketID,
			ChallengedResultTranscriptHash: challengedResultTranscriptHash,
			RecheckTranscriptHash:          recheckTranscriptHash,
			RecheckResultClass:             recheckResultClass,
			Details:                        details,
		}, nil
	})
}

func (m *module) ClaimHealComplete(
	ctx context.Context,
	healOpID uint64,
	ticketID string,
	healManifestHash string,
	details string,
) (*sdktx.BroadcastTxResponse, error) {
	if healOpID == 0 {
		return nil, fmt.Errorf("heal op id cannot be zero")
	}
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return nil, fmt.Errorf("ticket id cannot be empty")
	}
	healManifestHash = strings.TrimSpace(healManifestHash)
	if healManifestHash == "" {
		return nil, fmt.Errorf("heal manifest hash cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgClaimHealComplete{
			Creator:          creator,
			HealOpId:         healOpID,
			TicketId:         ticketID,
			HealManifestHash: healManifestHash,
			Details:          details,
		}, nil
	})
}

func (m *module) SubmitHealVerification(
	ctx context.Context,
	healOpID uint64,
	verified bool,
	verificationHash string,
	details string,
) (*sdktx.BroadcastTxResponse, error) {
	if healOpID == 0 {
		return nil, fmt.Errorf("heal op id cannot be zero")
	}
	verificationHash = strings.TrimSpace(verificationHash)
	if verificationHash == "" {
		return nil, fmt.Errorf("verification hash cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.txHelper.ExecuteTransaction(ctx, func(creator string) (sdktypes.Msg, error) {
		return &audittypes.MsgSubmitHealVerification{
			Creator:          creator,
			HealOpId:         healOpID,
			Verified:         verified,
			VerificationHash: verificationHash,
			Details:          details,
		}, nil
	})
}
