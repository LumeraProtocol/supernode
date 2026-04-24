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
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/go-bip39"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubAuditModule struct {
	currentEpoch   *audittypes.QueryCurrentEpochResponse
	anchor         *audittypes.QueryEpochAnchorResponse
	epochReportErr error
	assigned       *audittypes.QueryAssignedTargetsResponse
}

func (s *stubAuditModule) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	return &audittypes.QueryParamsResponse{}, nil
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
	return &audittypes.QueryEpochReportResponse{}, nil
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
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(7), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, obs []*audittypes.StorageChallengeObservation) (*sdktx.BroadcastTxResponse, error) {
			if len(obs) != 2 {
				t.Fatalf("expected 2 observations, got %d", len(obs))
			}
			for _, o := range obs {
				if o == nil || o.TargetSupernodeAccount == "" || len(o.PortStates) != 1 {
					t.Fatalf("invalid observation: %+v", o)
				}
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
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), uint64(8), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ uint64, _ audittypes.HostReport, obs []*audittypes.StorageChallengeObservation) (*sdktx.BroadcastTxResponse, error) {
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
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

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
	auditMsg.EXPECT().SubmitEpochReport(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	svc, err := NewService(identity, client, kr, keyName, "", "")
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	svc.tick(context.Background())
}
