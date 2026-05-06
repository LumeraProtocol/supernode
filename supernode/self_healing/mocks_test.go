package self_healing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	query "github.com/cosmos/cosmos-sdk/types/query"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

// programmableAudit is a per-test programmable audit module. The dispatcher
// reads only GetParams, GetHealOp, and GetHealOpsByStatus, so other methods
// are unused and may be left zero.
type programmableAudit struct {
	mu          sync.Mutex
	params      audittypes.Params
	opsByStatus map[audittypes.HealOpStatus][]audittypes.HealOp
	opsByID     map[uint64]audittypes.HealOp
	getOpErr    error
	blockStatus map[audittypes.HealOpStatus]bool
}

func newProgrammableAudit(mode audittypes.StorageTruthEnforcementMode) *programmableAudit {
	return &programmableAudit{
		params: audittypes.Params{
			StorageTruthEnforcementMode: mode,
		},
		opsByStatus: map[audittypes.HealOpStatus][]audittypes.HealOp{},
		opsByID:     map[uint64]audittypes.HealOp{},
		blockStatus: map[audittypes.HealOpStatus]bool{},
	}
}

func (p *programmableAudit) put(op audittypes.HealOp) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opsByID[op.HealOpId] = op
	p.opsByStatus[op.Status] = append(p.opsByStatus[op.Status], op)
}

func (p *programmableAudit) setStatus(opID uint64, st audittypes.HealOpStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	op := p.opsByID[opID]
	op.Status = st
	p.opsByID[opID] = op
}

func (p *programmableAudit) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &audittypes.QueryParamsResponse{Params: p.params}, nil
}
func (p *programmableAudit) GetHealOp(ctx context.Context, healOpID uint64) (*audittypes.QueryHealOpResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.getOpErr != nil {
		return nil, p.getOpErr
	}
	op, ok := p.opsByID[healOpID]
	if !ok {
		return nil, errors.New("not found")
	}
	return &audittypes.QueryHealOpResponse{HealOp: op}, nil
}
func (p *programmableAudit) GetHealOpsByStatus(ctx context.Context, status audittypes.HealOpStatus, pagination *query.PageRequest) (*audittypes.QueryHealOpsByStatusResponse, error) {
	p.mu.Lock()
	block := p.blockStatus[status]
	p.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]audittypes.HealOp, 0, len(p.opsByStatus[status]))
	for _, op := range p.opsByStatus[status] {
		out = append(out, op)
	}
	return &audittypes.QueryHealOpsByStatusResponse{HealOps: out}, nil
}
func (p *programmableAudit) GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*audittypes.QueryHealOpsByTicketResponse, error) {
	return &audittypes.QueryHealOpsByTicketResponse{}, nil
}
func (p *programmableAudit) GetEpochAnchor(ctx context.Context, epochID uint64) (*audittypes.QueryEpochAnchorResponse, error) {
	return &audittypes.QueryEpochAnchorResponse{}, nil
}
func (p *programmableAudit) GetCurrentEpochAnchor(ctx context.Context) (*audittypes.QueryCurrentEpochAnchorResponse, error) {
	return &audittypes.QueryCurrentEpochAnchorResponse{}, nil
}
func (p *programmableAudit) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	return &audittypes.QueryCurrentEpochResponse{}, nil
}
func (p *programmableAudit) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryAssignedTargetsResponse, error) {
	return &audittypes.QueryAssignedTargetsResponse{}, nil
}
func (p *programmableAudit) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	return &audittypes.QueryEpochReportResponse{}, nil
}
func (p *programmableAudit) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error) {
	return &audittypes.QueryEpochReportsByReporterResponse{}, nil
}
func (p *programmableAudit) GetNodeSuspicionState(ctx context.Context, supernodeAccount string) (*audittypes.QueryNodeSuspicionStateResponse, error) {
	return &audittypes.QueryNodeSuspicionStateResponse{}, nil
}
func (p *programmableAudit) GetReporterReliabilityState(ctx context.Context, reporterAccount string) (*audittypes.QueryReporterReliabilityStateResponse, error) {
	return &audittypes.QueryReporterReliabilityStateResponse{}, nil
}
func (p *programmableAudit) GetTicketDeteriorationState(ctx context.Context, ticketID string) (*audittypes.QueryTicketDeteriorationStateResponse, error) {
	return &audittypes.QueryTicketDeteriorationStateResponse{}, nil
}

// programmableAuditMsg captures every claim/verification call so tests can
// assert on the exact arguments the dispatcher used (e.g. that
// VerificationHash matches op.ResultHash and never Action.DataHash).
type programmableAuditMsg struct {
	mu                 sync.Mutex
	claimCalls         []claimCall
	verificationCalls  []verificationCall
	claimErr           error
	verificationErr    error
	claimsCount        atomic.Int64
	verificationsCount atomic.Int64
}

type claimCall struct {
	HealOpID         uint64
	TicketID         string
	HealManifestHash string
	Details          string
}

type verificationCall struct {
	HealOpID         uint64
	Verified         bool
	VerificationHash string
	Details          string
}

func newProgrammableAuditMsg() *programmableAuditMsg { return &programmableAuditMsg{} }

func (p *programmableAuditMsg) ClaimHealComplete(ctx context.Context, healOpID uint64, ticketID, healManifestHash, details string) (*sdktx.BroadcastTxResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimErr != nil {
		return nil, p.claimErr
	}
	p.claimCalls = append(p.claimCalls, claimCall{healOpID, ticketID, healManifestHash, details})
	p.claimsCount.Add(1)
	return &sdktx.BroadcastTxResponse{}, nil
}
func (p *programmableAuditMsg) SubmitHealVerification(ctx context.Context, healOpID uint64, verified bool, verificationHash, details string) (*sdktx.BroadcastTxResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verificationErr != nil {
		return nil, p.verificationErr
	}
	p.verificationCalls = append(p.verificationCalls, verificationCall{healOpID, verified, verificationHash, details})
	p.verificationsCount.Add(1)
	return &sdktx.BroadcastTxResponse{}, nil
}
func (p *programmableAuditMsg) SubmitEvidence(ctx context.Context, subjectAddress string, evidenceType audittypes.EvidenceType, actionID string, metadataJSON string) (*sdktx.BroadcastTxResponse, error) {
	return &sdktx.BroadcastTxResponse{}, nil
}
func (p *programmableAuditMsg) SubmitEpochReport(ctx context.Context, epochID uint64, hostReport audittypes.HostReport, storageChallengeObservations []*audittypes.StorageChallengeObservation, storageProofResults []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
	return &sdktx.BroadcastTxResponse{}, nil
}
func (p *programmableAuditMsg) SubmitStorageRecheckEvidence(ctx context.Context, epochID uint64, challengedSupernodeAccount string, ticketID string, challengedResultTranscriptHash string, recheckTranscriptHash string, recheckResultClass audittypes.StorageProofResultClass, details string) (*sdktx.BroadcastTxResponse, error) {
	return &sdktx.BroadcastTxResponse{}, nil
}

func (p *programmableAuditMsg) snapshot() ([]claimCall, []verificationCall) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := append([]claimCall(nil), p.claimCalls...)
	v := append([]verificationCall(nil), p.verificationCalls...)
	return c, v
}
