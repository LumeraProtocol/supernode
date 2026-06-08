package host_reporter

import (
	"context"
	"errors"
	"testing"
	"time"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	auditmsgmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit_msg"
	nodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	supernodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/query"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/go-bip39"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubAuditModule struct {
	currentEpoch   *audittypes.QueryCurrentEpochResponse
	anchor         *audittypes.QueryEpochAnchorResponse
	epochReport    *audittypes.QueryEpochReportResponse
	epochReportErr error
	assigned       *audittypes.QueryAssignedTargetsResponse
	params         audittypes.Params
}

func (s *stubAuditModule) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	return &audittypes.QueryParamsResponse{Params: s.params}, nil
}
func (s *stubAuditModule) GetEpochAnchor(ctx context.Context, epochID uint64) (*audittypes.QueryEpochAnchorResponse, error) {
	return s.anchor, nil
}
func (s *stubAuditModule) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	return s.currentEpoch, nil
}
func (s *stubAuditModule) GetCurrentEpochAnchor(ctx context.Context) (*audittypes.QueryCurrentEpochAnchorResponse, error) {
	return &audittypes.QueryCurrentEpochAnchorResponse{}, nil
}
func (s *stubAuditModule) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryAssignedTargetsResponse, error) {
	return s.assigned, nil
}
func (s *stubAuditModule) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	if s.epochReportErr != nil {
		return nil, s.epochReportErr
	}
	return s.epochReport, nil
}
func (s *stubAuditModule) GetEpochReportsByReporter(ctx context.Context, reporterAccount string, epochID uint64) (*audittypes.QueryEpochReportsByReporterResponse, error) {
	return &audittypes.QueryEpochReportsByReporterResponse{}, nil
}
func (s *stubAuditModule) GetNodeSuspicionState(ctx context.Context, supernodeAccount string) (*audittypes.QueryNodeSuspicionStateResponse, error) {
	return &audittypes.QueryNodeSuspicionStateResponse{}, nil
}
func (s *stubAuditModule) GetReporterReliabilityState(ctx context.Context, reporterAccount string) (*audittypes.QueryReporterReliabilityStateResponse, error) {
	return &audittypes.QueryReporterReliabilityStateResponse{}, nil
}
func (s *stubAuditModule) GetTicketDeteriorationState(ctx context.Context, ticketID string) (*audittypes.QueryTicketDeteriorationStateResponse, error) {
	return &audittypes.QueryTicketDeteriorationStateResponse{}, nil
}
func (s *stubAuditModule) GetHealOp(ctx context.Context, healOpID uint64) (*audittypes.QueryHealOpResponse, error) {
	return &audittypes.QueryHealOpResponse{}, nil
}
func (s *stubAuditModule) GetHealOpsByStatus(ctx context.Context, status audittypes.HealOpStatus, pagination *query.PageRequest) (*audittypes.QueryHealOpsByStatusResponse, error) {
	return &audittypes.QueryHealOpsByStatusResponse{}, nil
}
func (s *stubAuditModule) GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*audittypes.QueryHealOpsByTicketResponse, error) {
	return &audittypes.QueryHealOpsByTicketResponse{}, nil
}

func testKeyringAndIdentity(t *testing.T) (keyring.Keyring, string, string) {
	t.Helper()
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	kr := keyring.NewInMemory(cdc)

	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		t.Fatalf("entropy: %v", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		t.Fatalf("mnemonic: %v", err)
	}
	algoList, _ := kr.SupportedAlgorithms()
	signingAlgo, err := keyring.NewSigningAlgoFromString("secp256k1", algoList)
	if err != nil {
		t.Fatalf("signing algo: %v", err)
	}
	hdPath := hd.CreateHDPath(118, 0, 0).String()
	rec, err := kr.NewAccount("test", mnemonic, "", hdPath, signingAlgo)
	if err != nil {
		t.Fatalf("new account: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get addr: %v", err)
	}
	return kr, "test", addr.String()
}

func TestTick_ProberSubmitsObservationsForAssignedTargets(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 7},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 7}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: []string{"snA", "snB"},
			RequiredOpenPorts:       []uint32{4444},
		},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)

	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snA").Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)
	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snB").Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(7), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, obs []*audittypes.StorageChallengeObservation, proofs []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
			if len(obs) != 2 {
				t.Fatalf("expected 2 observations, got %d", len(obs))
			}
			for _, o := range obs {
				if o == nil || o.TargetSupernodeAccount == "" || len(o.PortStates) != 1 {
					t.Fatalf("invalid observation: %+v", o)
				}
			}
			if len(proofs) != 0 {
				t.Fatalf("expected 0 proof results when no provider attached, got %d", len(proofs))
			}
			return &sdktx.BroadcastTxResponse{}, nil
		},
	)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.dialTimeout = 10 * time.Millisecond
	svc.tick(context.Background())
}

func TestTick_NonProberSubmitsHostOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 8},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 8}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: nil,
			RequiredOpenPorts:       []uint32{4444, 4445},
		},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(8), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, obs []*audittypes.StorageChallengeObservation, _ []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
			if len(obs) != 0 {
				t.Fatalf("expected 0 observations for non-prober, got %d", len(obs))
			}
			return &sdktx.BroadcastTxResponse{}, nil
		},
	)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.tick(context.Background())
}

func TestTick_SkipsWhenEpochAlreadyReported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 9},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 9}},
		epochReportErr: nil,
		assigned:       &audittypes.QueryAssignedTargetsResponse{},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.tick(context.Background())
}

func TestTick_SkipsOnEpochReportLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 10},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 10}},
		epochReportErr: errors.New("rpc unavailable"),
		assigned:       &audittypes.QueryAssignedTargetsResponse{},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.tick(context.Background())
}

// stubProofResultProvider records the epoch it was queried with and returns a
// fixed slice of synthetic StorageProofResult records.
type stubProofResultProvider struct {
	queriedEpochs  []uint64
	requeuedEpochs []uint64
	results        []*audittypes.StorageProofResult
}

func (s *stubProofResultProvider) CollectResults(epochID uint64) []*audittypes.StorageProofResult {
	s.queriedEpochs = append(s.queriedEpochs, epochID)
	return s.results
}

func (s *stubProofResultProvider) RequeueResults(epochID uint64, results []*audittypes.StorageProofResult) {
	s.requeuedEpochs = append(s.requeuedEpochs, epochID)
	s.results = append([]*audittypes.StorageProofResult(nil), results...)
}

func TestTick_AttachedProofResultProviderIsDrainedAndForwarded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 11},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 11}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned:       &audittypes.QueryAssignedTargetsResponse{},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)

	provider := &stubProofResultProvider{
		results: []*audittypes.StorageProofResult{
			{TargetSupernodeAccount: "snA", TicketId: "ticket-1", TranscriptHash: "hash-1"},
			{TargetSupernodeAccount: "snB", TicketId: "ticket-2", TranscriptHash: "hash-2"},
		},
	}

	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(11), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, _ []*audittypes.StorageChallengeObservation, proofs []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
			if len(proofs) != 2 {
				t.Fatalf("expected 2 proof results from provider, got %d", len(proofs))
			}
			if proofs[0].TicketId != "ticket-1" || proofs[1].TicketId != "ticket-2" {
				t.Fatalf("proof results not forwarded verbatim: %+v", proofs)
			}
			return &sdktx.BroadcastTxResponse{}, nil
		},
	)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetProofResultProvider(provider)
	svc.tick(context.Background())

	if len(provider.queriedEpochs) != 1 || provider.queriedEpochs[0] != 11 {
		t.Fatalf("expected provider queried once for epoch 11, got %v", provider.queriedEpochs)
	}
}

// TestTick_SHADOWModeSubmitsEmptyProofs is the LEP-6 PR286 F1 regression:
// in SHADOW the chain only enforces compound proof coverage in FULL mode
// (see lumera x/audit/v1/keeper/msg_submit_epoch_report.go:143). The host
// reporter MUST submit the epoch report even when local LEP-6 proof rows
// are empty, otherwise it stops sending host/peer observations entirely
// and feeds the audit_missing_reports postponement path.
func TestTick_SHADOWModeSubmitsEmptyProofs(t *testing.T) {
	testTickSubmitsEmptyProofsForMode(t, audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)
}

// TestTick_SOFTModeSubmitsEmptyProofs covers the same F1 fix as SHADOW —
// SOFT is also an observational mode and chain accepts empty proof rows.
func TestTick_SOFTModeSubmitsEmptyProofs(t *testing.T) {
	testTickSubmitsEmptyProofsForMode(t, audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SOFT)
}

func testTickSubmitsEmptyProofsForMode(t *testing.T, mode audittypes.StorageTruthEnforcementMode) {
	t.Helper()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 13},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 13}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: []string{"snA"},
		},
		params: audittypes.Params{StorageTruthEnforcementMode: mode},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snA").AnyTimes().Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)

	provider := &stubProofResultProvider{}
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(13), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, _ []*audittypes.StorageChallengeObservation, proofs []*audittypes.StorageProofResult) (*sdktx.BroadcastTxResponse, error) {
			if len(proofs) != 0 {
				t.Fatalf("expected empty proof results in mode %s, got %d", mode, len(proofs))
			}
			return &sdktx.BroadcastTxResponse{}, nil
		},
	).Times(1)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetProofResultProvider(provider)
	svc.dialTimeout = 10 * time.Millisecond
	svc.tick(context.Background())

	if len(provider.requeuedEpochs) != 0 {
		t.Fatalf("expected no requeue when proofs were submitted (empty is fine in %s mode), got %v", mode, provider.requeuedEpochs)
	}
}

// TestTick_SubmitFailureRequeuesProofResults covers the LEP-6 PR286 F2
// regression: CollectResults destructively drains the proof buffer; if
// SubmitEpochReport then fails with anything other than a chain duplicate,
// the drained rows MUST be requeued so the next tick can retry them.
func TestTick_SubmitFailureRequeuesProofResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 14},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 14}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: []string{"snA"},
		},
		// SHADOW so an empty-coverage drain still reaches Submit (FULL would
		// short-circuit on the coverage gate).
		params: audittypes.Params{StorageTruthEnforcementMode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snA").AnyTimes().Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)

	drained := []*audittypes.StorageProofResult{{
		TargetSupernodeAccount: "snA",
		TicketId:               "ticket-14",
		TranscriptHash:         "hash-14",
	}}
	provider := &stubProofResultProvider{results: drained}

	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(14), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, errors.New("rpc unavailable: connect: connection refused")).
		Times(1)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetProofResultProvider(provider)
	svc.dialTimeout = 10 * time.Millisecond
	svc.tick(context.Background())

	if len(provider.requeuedEpochs) != 1 || provider.requeuedEpochs[0] != 14 {
		t.Fatalf("expected drained proofs requeued on submit failure for epoch 14, got %v", provider.requeuedEpochs)
	}
	if len(provider.results) != 1 || provider.results[0].TicketId != "ticket-14" {
		t.Fatalf("expected requeued proof rows preserved verbatim, got %+v", provider.results)
	}
}

// TestTick_DuplicateReportErrorDoesNotRequeue ensures that when chain
// returns ErrDuplicateReport (report already submitted for this epoch),
// the drained proof rows are NOT requeued — they are stale and another
// submit would just be rejected again. This is the "do not requeue stale
// rows" branch of the F2 fix.
func TestTick_DuplicateReportErrorDoesNotRequeue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 15},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 15}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: []string{"snA"},
		},
		params: audittypes.Params{StorageTruthEnforcementMode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snA").AnyTimes().Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)

	provider := &stubProofResultProvider{results: []*audittypes.StorageProofResult{{
		TargetSupernodeAccount: "snA",
		TicketId:               "ticket-15",
		TranscriptHash:         "hash-15",
	}}}

	// Match the chain phrase from lumera x/audit/v1/keeper/msg_submit_epoch_report.go:142.
	dupErr := errors.New("report already submitted for this epoch")
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(15), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, dupErr).
		Times(1)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetProofResultProvider(provider)
	svc.dialTimeout = 10 * time.Millisecond
	svc.tick(context.Background())

	if len(provider.requeuedEpochs) != 0 {
		t.Fatalf("expected NO requeue on chain-duplicate response, got %v", provider.requeuedEpochs)
	}
}

func TestTick_FULLModeIncompleteStorageProofCoverageSkipsSubmitAndRequeues(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	kr, keyName, identity := testKeyringAndIdentity(t)
	auditMod := &stubAuditModule{
		currentEpoch:   &audittypes.QueryCurrentEpochResponse{EpochId: 12},
		anchor:         &audittypes.QueryEpochAnchorResponse{Anchor: audittypes.EpochAnchor{EpochId: 12}},
		epochReportErr: status.Error(codes.NotFound, "not found"),
		assigned: &audittypes.QueryAssignedTargetsResponse{
			TargetSupernodeAccounts: []string{"snA"},
			RequiredOpenPorts:       nil,
		},
		params: audittypes.Params{StorageTruthEnforcementMode: audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL},
	}
	auditMsg := auditmsgmod.NewMockModule(ctrl)
	node := nodemod.NewMockModule(ctrl)
	sn := supernodemod.NewMockModule(ctrl)
	client := lumeraMock.NewMockClient(ctrl)
	client.EXPECT().Audit().AnyTimes().Return(auditMod)
	client.EXPECT().AuditMsg().AnyTimes().Return(auditMsg)
	client.EXPECT().SuperNode().AnyTimes().Return(sn)
	client.EXPECT().Node().AnyTimes().Return(node)
	sn.EXPECT().GetSupernodeWithLatestAddress(gomock.Any(), "snA").Return(&supernodemod.SuperNodeInfo{LatestAddress: "127.0.0.1:4444"}, nil)
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	provider := &stubProofResultProvider{results: []*audittypes.StorageProofResult{
		{TargetSupernodeAccount: "snA", BucketType: audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT, TicketId: "ticket-recent", TranscriptHash: "hash-recent"},
	}}

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.SetProofResultProvider(provider)
	svc.tick(context.Background())

	if len(provider.queriedEpochs) != 1 || provider.queriedEpochs[0] != 12 {
		t.Fatalf("expected provider queried once for epoch 12, got %v", provider.queriedEpochs)
	}
	if len(provider.requeuedEpochs) != 1 || provider.requeuedEpochs[0] != 12 {
		t.Fatalf("expected incomplete FULL proofs requeued for epoch 12, got %v", provider.requeuedEpochs)
	}
}
