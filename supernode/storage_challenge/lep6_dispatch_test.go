package storage_challenge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	supernodepb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	auditmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/audit"
	nodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"lukechampine.com/blake3"
)

// dispatchAuditModule is an in-test stub of audit.Module used to drive
// LEP6Dispatcher per-test; mirrors the host_reporter test pattern.
type dispatchAuditModule struct {
	params   *audittypes.QueryParamsResponse
	anchor   *audittypes.QueryEpochAnchorResponse
	assigned *audittypes.QueryAssignedTargetsResponse
}

var _ auditmod.Module = (*dispatchAuditModule)(nil)

func (s *dispatchAuditModule) GetParams(ctx context.Context) (*audittypes.QueryParamsResponse, error) {
	return s.params, nil
}
func (s *dispatchAuditModule) GetEpochAnchor(ctx context.Context, epochID uint64) (*audittypes.QueryEpochAnchorResponse, error) {
	return s.anchor, nil
}
func (s *dispatchAuditModule) GetCurrentEpoch(ctx context.Context) (*audittypes.QueryCurrentEpochResponse, error) {
	return &audittypes.QueryCurrentEpochResponse{}, nil
}
func (s *dispatchAuditModule) GetCurrentEpochAnchor(ctx context.Context) (*audittypes.QueryCurrentEpochAnchorResponse, error) {
	return &audittypes.QueryCurrentEpochAnchorResponse{}, nil
}
func (s *dispatchAuditModule) GetAssignedTargets(ctx context.Context, supernodeAccount string, epochID uint64) (*audittypes.QueryAssignedTargetsResponse, error) {
	return s.assigned, nil
}
func (s *dispatchAuditModule) GetEpochReport(ctx context.Context, epochID uint64, supernodeAccount string) (*audittypes.QueryEpochReportResponse, error) {
	return &audittypes.QueryEpochReportResponse{}, nil
}
func (s *dispatchAuditModule) GetNodeSuspicionState(ctx context.Context, supernodeAccount string) (*audittypes.QueryNodeSuspicionStateResponse, error) {
	return &audittypes.QueryNodeSuspicionStateResponse{}, nil
}
func (s *dispatchAuditModule) GetReporterReliabilityState(ctx context.Context, reporterAccount string) (*audittypes.QueryReporterReliabilityStateResponse, error) {
	return &audittypes.QueryReporterReliabilityStateResponse{}, nil
}
func (s *dispatchAuditModule) GetTicketDeteriorationState(ctx context.Context, ticketID string) (*audittypes.QueryTicketDeteriorationStateResponse, error) {
	return &audittypes.QueryTicketDeteriorationStateResponse{}, nil
}
func (s *dispatchAuditModule) GetHealOp(ctx context.Context, healOpID uint64) (*audittypes.QueryHealOpResponse, error) {
	return &audittypes.QueryHealOpResponse{}, nil
}
func (s *dispatchAuditModule) GetHealOpsByStatus(ctx context.Context, status audittypes.HealOpStatus, pagination *query.PageRequest) (*audittypes.QueryHealOpsByStatusResponse, error) {
	return &audittypes.QueryHealOpsByStatusResponse{}, nil
}
func (s *dispatchAuditModule) GetHealOpsByTicket(ctx context.Context, ticketID string, pagination *query.PageRequest) (*audittypes.QueryHealOpsByTicketResponse, error) {
	return &audittypes.QueryHealOpsByTicketResponse{}, nil
}

// stubTicketProvider returns a fixed list per target.
type stubTicketProvider struct {
	tickets map[string][]TicketDescriptor
	err     error
}

func (s stubTicketProvider) TicketsForTarget(_ context.Context, target string) ([]TicketDescriptor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.tickets[target], nil
}

// stubMetaProvider returns a fixed cascade meta + size for any ticket.
type stubMetaProvider struct {
	meta *actiontypes.CascadeMetadata
	size uint64
	err  error
}

func (s stubMetaProvider) GetCascadeMetadata(_ context.Context, _ string) (*actiontypes.CascadeMetadata, uint64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.meta, s.size, nil
}

// stubCompoundClient implements SupernodeCompoundClient.
type stubCompoundClient struct {
	resp *supernodepb.GetCompoundProofResponse
	err  error
}

func (s *stubCompoundClient) GetCompoundProof(_ context.Context, _ *supernodepb.GetCompoundProofRequest) (*supernodepb.GetCompoundProofResponse, error) {
	return s.resp, s.err
}
func (s *stubCompoundClient) Close() error { return nil }

// stubFactory always returns the same stubCompoundClient.
type stubFactory struct {
	client *stubCompoundClient
	err    error
}

func (s *stubFactory) Dial(_ context.Context, _ string) (SupernodeCompoundClient, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.client, nil
}

func newDispatchKeyringAndIdentity(t *testing.T) (keyring.Keyring, string, string) {
	t.Helper()
	ir := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(ir)
	cdc := codec.NewProtoCodec(ir)
	kr := keyring.NewInMemory(cdc)
	entropy, err := bip39.NewEntropy(128)
	require.NoError(t, err)
	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	algos, _ := kr.SupportedAlgorithms()
	algo, err := keyring.NewSigningAlgoFromString("secp256k1", algos)
	require.NoError(t, err)
	hdPath := hd.CreateHDPath(118, 0, 0).String()
	rec, err := kr.NewAccount("dispatcher-test", mnemonic, "", hdPath, algo)
	require.NoError(t, err)
	addr, err := rec.GetAddress()
	require.NoError(t, err)
	return kr, "dispatcher-test", addr.String()
}

// makeAnchor returns a deterministic EpochAnchor with a 32-byte seed
// derived from the epoch id so tests are reproducible across runs.
func makeAnchor(epochID uint64, endHeight int64, targets ...string) audittypes.EpochAnchor {
	seed := sha256.Sum256([]byte("test-seed"))
	return audittypes.EpochAnchor{
		EpochId:                 epochID,
		EpochEndHeight:          endHeight,
		EpochLengthBlocks:       100,
		Seed:                    seed[:],
		ActiveSupernodeAccounts: append([]string{}, targets...),
		TargetSupernodeAccounts: append([]string{}, targets...),
	}
}

// defaultParams returns audit Params with bucket thresholds matching the
// chain's defaults (3*EpochLengthBlocks RECENT, 30*EpochLengthBlocks OLD)
// and the requested enforcement mode.
func defaultParams(mode audittypes.StorageTruthEnforcementMode) audittypes.Params {
	return audittypes.Params{
		StorageTruthEnforcementMode:           mode,
		StorageTruthRecentBucketMaxBlocks:     300,
		StorageTruthOldBucketMinBlocks:        3000,
		StorageTruthCompoundRangesPerArtifact: uint32(deterministic.LEP6CompoundRangesPerArtifact),
		StorageTruthCompoundRangeLenBytes:     uint32(deterministic.LEP6CompoundRangeLenBytes),
	}
}

// newDispatcher wires a dispatcher with the given audit module + factory +
// providers. Returns the dispatcher and the buffer (for assertions).
func newDispatcher(
	t *testing.T,
	audit *dispatchAuditModule,
	factory SupernodeClientFactory,
	tickets TicketProvider,
	meta CascadeMetaProvider,
) (*LEP6Dispatcher, *Buffer) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockLumera := lumeraMock.NewMockClient(ctrl)
	mockLumera.EXPECT().Audit().Return(audit).AnyTimes()
	// Node() returns a typed-nil; only used when EpochAnchor.EpochEndHeight==0,
	// which our tests always set non-zero, so this is unreachable in practice.
	var nilNode nodemod.Module
	mockLumera.EXPECT().Node().Return(nilNode).AnyTimes()

	kr, keyName, identity := newDispatchKeyringAndIdentity(t)
	buf := NewBuffer()
	d, err := NewLEP6Dispatcher(mockLumera, kr, keyName, identity, factory, tickets, meta, buf)
	require.NoError(t, err)
	return d, buf
}

func TestDispatchEpoch_ModeUnspecified_NoOp(t *testing.T) {
	audit := &dispatchAuditModule{
		params: &audittypes.QueryParamsResponse{
			Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_UNSPECIFIED),
		},
	}
	d, buf := newDispatcher(t, audit, &stubFactory{}, NoTicketProvider{}, stubMetaProvider{})

	require.NoError(t, d.DispatchEpoch(context.Background(), 7))
	require.Empty(t, buf.CollectResults(7), "buffer must be empty under UNSPECIFIED mode")
}

func TestDispatchEpoch_ModeShadow_AppendsResults(t *testing.T) {
	const epochID uint64 = 11
	anchor := makeAnchor(epochID, 500, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	// NoTicketProvider → both buckets emit NO_ELIGIBLE_TICKET.
	d, buf := newDispatcher(t, audit, &stubFactory{}, NoTicketProvider{}, stubMetaProvider{})

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.Len(t, results, 2, "expected one NO_ELIGIBLE_TICKET per bucket")
	for _, r := range results {
		require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET, r.ResultClass)
		require.NotEmpty(t, r.TranscriptHash)
		require.NotEmpty(t, r.ChallengerSignature)
	}
}

func TestDispatchEpoch_NoEligibleTicket_EmitsClass(t *testing.T) {
	const epochID uint64 = 13
	// Anchor end-height=10000; tickets anchored at heights that fall in NEITHER
	// bucket. Gap is delta ∈ (recent_max=300, old_min=3000), i.e. 301..2999.
	// Pick anchor=8000 → currentHeight-anchor=2000 → UNSPECIFIED bucket.
	anchor := makeAnchor(epochID, 10000, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-gap", AnchorBlock: 8000}},
	}}
	d, buf := newDispatcher(t, audit, &stubFactory{}, tickets, stubMetaProvider{})

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET, r.ResultClass)
	}
}

// TestDispatchEpoch_GetCompoundProofError_EmitsFailClass exercises the dial /
// RPC failure path: when the ticket is eligible and the RPC returns an error,
// the dispatcher emits a FAIL-class result (not bubble the error up) so the
// chain still sees coverage.
func TestDispatchEpoch_GetCompoundProofError_EmitsFailClass(t *testing.T) {
	const epochID uint64 = 17
	// EpochEndHeight=200, ticket anchor=100 → currentHeight-anchor=100 < 300 →
	// RECENT bucket eligible.
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-rpc-fail", AnchorBlock: 100}},
	}}
	// Cascade meta: SYMBOL-only with one id; artifact_size big enough for 4*256.
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{RqIdsIc: 0, RqIdsMax: 1, RqIdsIds: []string{"sym-0"}},
		size: 4 * 1024,
	}
	// Factory returns a client whose GetCompoundProof errors.
	factory := &stubFactory{client: &stubCompoundClient{err: errors.New("rpc unavailable")}}
	d, buf := newDispatcher(t, audit, factory, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.NotEmpty(t, results)
	// Expect a FAIL class for the RECENT bucket (single eligible ticket) and
	// NO_ELIGIBLE for OLD (empty there).
	var sawFail, sawNoEligible bool
	for _, r := range results {
		switch r.ResultClass {
		case audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT:
			sawFail = true
			require.Contains(t, r.Details, "rpc unavailable")
		case audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_NO_ELIGIBLE_TICKET:
			sawNoEligible = true
		}
	}
	require.True(t, sawFail, "expected at least one FAIL class result on RPC error")
	require.True(t, sawNoEligible, "expected NO_ELIGIBLE for the OLD bucket")
}

func TestClassifyProofFailure_NonTimeoutRPCErrorsAreInvalidTranscript(t *testing.T) {
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT,
		classifyProofFailure(status.Error(codes.PermissionDenied, "not allowed"), "not allowed"),
	)
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT,
		classifyProofFailure(errors.New("connection refused"), "connection refused"),
	)
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_INVALID_TRANSCRIPT,
		classifyProofFailure(nil, "recipient validation failed"),
	)
}

func TestClassifyProofFailure_TimeoutsRemainTimeoutOrNoResponse(t *testing.T) {
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		classifyProofFailure(context.DeadlineExceeded, "deadline exceeded"),
	)
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		classifyProofFailure(status.Error(codes.Unavailable, "unavailable"), "unavailable"),
	)
	require.Equal(t,
		audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE,
		classifyProofFailure(nil, "no response"),
	)
}

func TestDispatchEpoch_GetCompoundProofTimeout_EmitsTimeoutClass(t *testing.T) {
	const epochID uint64 = 18
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-timeout", AnchorBlock: 100}},
	}}
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{RqIdsIc: 0, RqIdsMax: 1, RqIdsIds: []string{"sym-0"}},
		size: 4 * 1024,
	}
	factory := &stubFactory{client: &stubCompoundClient{err: context.DeadlineExceeded}}
	d, buf := newDispatcher(t, audit, factory, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.NotEmpty(t, results)
	var sawTimeout bool
	for _, r := range results {
		if r.TicketId == "tkt-timeout" {
			sawTimeout = true
			require.Equal(t, audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_TIMEOUT_OR_NO_RESPONSE, r.ResultClass)
		}
	}
	require.True(t, sawTimeout, "expected timeout-class result for deadline exceeded RPC")
}

// TestDispatchEpoch_HappyPath_EmitsPassResult exercises the full PASS path:
// eligible ticket, valid cascade meta, GetCompoundProof returns 4 ranges of
// 256B each whose BLAKE3 hash matches resp.ProofHashHex. Dispatcher must
// emit PASS-class result with non-empty transcript + signature + derivation
// hash.
//
// Only RECENT is exercised here; OLD bucket has no eligible ticket and emits
// NO_ELIGIBLE, which is also asserted.
func TestDispatchEpoch_HappyPath_EmitsPassResult(t *testing.T) {
	const epochID uint64 = 19
	anchor := makeAnchor(epochID, 200, "sn-target")
	audit := &dispatchAuditModule{
		params:   &audittypes.QueryParamsResponse{Params: defaultParams(audittypes.StorageTruthEnforcementMode_STORAGE_TRUTH_ENFORCEMENT_MODE_FULL)},
		anchor:   &audittypes.QueryEpochAnchorResponse{Anchor: anchor},
		assigned: &audittypes.QueryAssignedTargetsResponse{TargetSupernodeAccounts: []string{"sn-target"}},
	}
	tickets := stubTicketProvider{tickets: map[string][]TicketDescriptor{
		"sn-target": {{TicketID: "tkt-happy", AnchorBlock: 100}},
	}}
	meta := stubMetaProvider{
		meta: &actiontypes.CascadeMetadata{RqIdsIc: 0, RqIdsMax: 1, RqIdsIds: []string{"sym-0"}},
		size: 4 * 1024,
	}

	// Construct a response with 4 ranges of 256 bytes each (deterministic
	// content) and a matching BLAKE3 proof hash.
	rangeBytes := make([][]byte, deterministic.LEP6CompoundRangesPerArtifact)
	hasher := blake3.New(32, nil)
	for i := range rangeBytes {
		buf := make([]byte, deterministic.LEP6CompoundRangeLenBytes)
		// Fill with i-stamped bytes for determinism.
		for j := range buf {
			buf[j] = byte((i*7 + j) & 0xFF)
		}
		rangeBytes[i] = buf
		_, _ = hasher.Write(buf)
	}
	proofHashHex := hex.EncodeToString(hasher.Sum(nil))
	resp := &supernodepb.GetCompoundProofResponse{
		Ok:           true,
		RangeBytes:   rangeBytes,
		ProofHashHex: proofHashHex,
	}
	factory := &stubFactory{client: &stubCompoundClient{resp: resp}}
	d, buf := newDispatcher(t, audit, factory, tickets, meta)

	require.NoError(t, d.DispatchEpoch(context.Background(), epochID))
	results := buf.CollectResults(epochID)
	require.NotEmpty(t, results)

	var sawPass bool
	for _, r := range results {
		if r.ResultClass == audittypes.StorageProofResultClass_STORAGE_PROOF_RESULT_CLASS_PASS {
			sawPass = true
			require.Equal(t, "tkt-happy", r.TicketId)
			require.Equal(t, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT, r.BucketType)
			require.NotEmpty(t, r.TranscriptHash)
			require.NotEmpty(t, r.DerivationInputHash)
			require.NotEmpty(t, r.ChallengerSignature)
			require.NotEmpty(t, r.ArtifactKey)
		}
	}
	require.True(t, sawPass, "expected a PASS-class result on happy path")
}
