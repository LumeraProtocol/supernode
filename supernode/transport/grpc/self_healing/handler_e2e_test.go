package self_healing

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	lumeraclient "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	actionmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
	"github.com/golang/protobuf/proto"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"lukechampine.com/blake3"
)

type fakeP2P struct {
	local   map[string][]byte
	network map[string][]byte
}

func newFakeP2P() *fakeP2P {
	return &fakeP2P{local: map[string][]byte{}, network: map[string][]byte{}}
}

func (f *fakeP2P) Retrieve(ctx context.Context, key string, localOnly ...bool) ([]byte, error) {
	if len(localOnly) > 0 && localOnly[0] {
		if v, ok := f.local[key]; ok {
			return append([]byte(nil), v...), nil
		}
		return nil, nil
	}
	if v, ok := f.local[key]; ok {
		return append([]byte(nil), v...), nil
	}
	if v, ok := f.network[key]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, nil
}

func (f *fakeP2P) BatchRetrieve(ctx context.Context, keys []string, reqCount int, txID string, localOnly ...bool) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for _, k := range keys {
		if v, _ := f.Retrieve(ctx, k, localOnly...); len(v) > 0 {
			out[k] = v
		}
	}
	return out, nil
}

func (f *fakeP2P) BatchRetrieveStream(ctx context.Context, keys []string, required int32, txID string, onSymbol func(base58Key string, data []byte) error, localOnly ...bool) (int32, error) {
	var n int32
	for _, k := range keys {
		v, _ := f.Retrieve(ctx, k, localOnly...)
		if len(v) == 0 {
			continue
		}
		if err := onSymbol(k, v); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (f *fakeP2P) Store(ctx context.Context, data []byte, typ int) (string, error) { return "", nil }
func (f *fakeP2P) StoreBatch(ctx context.Context, values [][]byte, typ int, taskID string) error {
	return nil
}
func (f *fakeP2P) Delete(ctx context.Context, key string) error          { return nil }
func (f *fakeP2P) Stats(ctx context.Context) (*p2p.StatsSnapshot, error) { return nil, nil }
func (f *fakeP2P) NClosestNodes(ctx context.Context, n int, key string, ignores ...string) []string {
	return nil
}
func (f *fakeP2P) NClosestNodesWithIncludingNodeList(ctx context.Context, n int, key string, ignores, nodesToInclude []string) []string {
	return nil
}
func (f *fakeP2P) LocalStore(ctx context.Context, key string, data []byte) (string, error) {
	f.local[key] = append([]byte(nil), data...)
	return key, nil
}
func (f *fakeP2P) DisableKey(ctx context.Context, b58EncodedHash string) error { return nil }
func (f *fakeP2P) EnableKey(ctx context.Context, b58EncodedHash string) error  { return nil }
func (f *fakeP2P) GetLocalKeys(ctx context.Context, from *time.Time, to time.Time) ([]string, error) {
	out := make([]string, 0, len(f.local))
	for k := range f.local {
		out = append(out, k)
	}
	return out, nil
}

type fakeCascadeTask struct {
	recoveryFn func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error)
}

func (f *fakeCascadeTask) Register(ctx context.Context, req *cascadeService.RegisterRequest, send func(resp *cascadeService.RegisterResponse) error) error {
	return nil
}

func (f *fakeCascadeTask) Download(ctx context.Context, req *cascadeService.DownloadRequest, send func(resp *cascadeService.DownloadResponse) error) error {
	return nil
}

func (f *fakeCascadeTask) CleanupDownload(ctx context.Context, tmpDir string) error {
	return nil
}

func (f *fakeCascadeTask) RecoveryReseed(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
	if f.recoveryFn != nil {
		return f.recoveryFn(ctx, req)
	}
	return &cascadeService.RecoveryReseedResult{ActionID: req.ActionID}, nil
}

type fakeCascadeFactory struct {
	task *fakeCascadeTask
}

func (f *fakeCascadeFactory) NewCascadeRegistrationTask() cascadeService.CascadeTask {
	return f.task
}

func mockLumeraActionLookup(t *testing.T, fileKey, actionID string) (lumeraclient.Client, func()) {
	t.Helper()
	ctrl := gomock.NewController(t)
	lumeraClient := lumeraclient.NewMockClient(ctrl)
	actionModule := actionmod.NewMockModule(ctrl)

	meta, err := proto.Marshal(&actiontypes.CascadeMetadata{
		RqIdsIds: []string{fileKey},
	})
	if err != nil {
		t.Fatalf("marshal cascade metadata: %v", err)
	}

	lumeraClient.EXPECT().Action().AnyTimes().Return(actionModule)
	lumeraClient.EXPECT().Node().AnyTimes().Return(nil)
	lumeraClient.EXPECT().Audit().AnyTimes().Return(nil)
	actionModule.EXPECT().ListActions(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, req *actiontypes.QueryListActionsRequest) (*actiontypes.QueryListActionsResponse, error) {
			if req == nil {
				return nil, fmt.Errorf("nil request")
			}
			return &actiontypes.QueryListActionsResponse{
				Actions: []*actiontypes.Action{
					{
						ActionID: actionID,
						Metadata: meta,
						State:    actiontypes.ActionStateDone,
					},
				},
			}, nil
		},
	)

	return lumeraClient, ctrl.Finish
}

func startSelfHealingTestServer(t *testing.T, identity string, p2p *fakeP2P, lumeraClient lumeraclient.Client, cascadeFactory cascadeService.CascadeServiceFactory) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	supernode.RegisterSelfHealingServiceServer(s, NewServer(identity, p2p, lumeraClient, nil, cascadeFactory))
	go func() { _ = s.Serve(lis) }()

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		s.Stop()
		_ = lis.Close()
	}
	return conn, cleanup
}

func TestSelfHealingE2E_RequestThenVerify(t *testing.T) {
	const fileKey = "key-1"
	const actionID = "action-1"
	payload := []byte("hello-self-healing")
	payloadHashHex := blake3Hex(payload)

	recipientP2P := newFakeP2P()
	recipientP2P.network[fileKey] = payload

	observerP2P := newFakeP2P()
	observerP2P.local[fileKey] = payload // observer has local authoritative copy

	lumeraClient, lumeraCleanup := mockLumeraActionLookup(t, fileKey, actionID)
	defer lumeraCleanup()
	factory := &fakeCascadeFactory{
		task: &fakeCascadeTask{
			recoveryFn: func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
				if req == nil || req.ActionID != actionID {
					return nil, fmt.Errorf("unexpected recovery action_id")
				}
				if req.PersistArtifacts != nil && !*req.PersistArtifacts {
					return &cascadeService.RecoveryReseedResult{
						ActionID:             actionID,
						ReconstructedHashHex: payloadHashHex,
					}, nil
				}
				recipientP2P.local[fileKey] = append([]byte(nil), payload...)
				return &cascadeService.RecoveryReseedResult{
					ActionID:             actionID,
					ReconstructedHashHex: payloadHashHex,
				}, nil
			},
		},
	}

	recConn, recCleanup := startSelfHealingTestServer(t, "recipient-1", recipientP2P, lumeraClient, factory)
	defer recCleanup()
	obsConn, obsCleanup := startSelfHealingTestServer(t, "observer-1", observerP2P, nil, nil)
	defer obsCleanup()

	recClient := supernode.NewSelfHealingServiceClient(recConn)
	obsClient := supernode.NewSelfHealingServiceClient(obsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := recClient.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-1",
		EpochId:      12,
		FileKey:      fileKey,
		ChallengerId: "challenger-1",
		RecipientId:  "recipient-1",
		ObserverIds:  []string{"observer-1"},
		ActionId:     actionID,
	})
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true, got false err=%s", resp.Error)
	}
	if !resp.ReconstructionRequired {
		t.Fatalf("expected reconstruction_required=true")
	}
	if got := recipientP2P.local[fileKey]; len(got) > 0 {
		t.Fatalf("expected no recipient local store before commit")
	}

	ver, err := obsClient.VerifySelfHealing(ctx, &supernode.VerifySelfHealingRequest{
		ChallengeId:          "ch-1",
		EpochId:              12,
		FileKey:              fileKey,
		RecipientId:          "recipient-1",
		ReconstructedHashHex: resp.ReconstructedHashHex,
		ObserverId:           "observer-1",
		ActionId:             actionID,
	})
	if err != nil {
		t.Fatalf("verify self-healing: %v", err)
	}
	if !ver.Ok {
		t.Fatalf("expected verify ok=true, got false err=%s", ver.Error)
	}
	commitResp, err := recClient.CommitSelfHealing(ctx, &supernode.CommitSelfHealingRequest{
		ChallengeId:  "ch-1",
		EpochId:      12,
		FileKey:      fileKey,
		ActionId:     actionID,
		ChallengerId: "challenger-1",
		RecipientId:  "recipient-1",
	})
	if err != nil {
		t.Fatalf("commit self-healing: %v", err)
	}
	if !commitResp.Stored {
		t.Fatalf("expected commit stored=true, got false err=%s", commitResp.Error)
	}
	if got := recipientP2P.local[fileKey]; string(got) != string(payload) {
		t.Fatalf("recipient local store not repaired after commit")
	}
}

func TestSelfHealingE2E_RequestFileNotRetrievable(t *testing.T) {
	const fileKey = "missing-key"

	recipientP2P := newFakeP2P() // file is absent both locally and on network
	recConn, recCleanup := startSelfHealingTestServer(t, "recipient-1", recipientP2P, nil, nil)
	defer recCleanup()

	recClient := supernode.NewSelfHealingServiceClient(recConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := recClient.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-missing",
		EpochId:      12,
		FileKey:      fileKey,
		ChallengerId: "challenger-1",
		RecipientId:  "recipient-1",
		ObserverIds:  []string{"observer-1"},
	})
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected accepted=false for non-retrievable file")
	}
}

func TestSelfHealingE2E_RequestRecipientMismatch(t *testing.T) {
	recipientP2P := newFakeP2P()
	recConn, recCleanup := startSelfHealingTestServer(t, "recipient-1", recipientP2P, nil, nil)
	defer recCleanup()

	recClient := supernode.NewSelfHealingServiceClient(recConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := recClient.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
		ChallengeId:  "ch-recipient-mismatch",
		EpochId:      12,
		FileKey:      "key-1",
		ChallengerId: "challenger-1",
		RecipientId:  "recipient-2",
		ObserverIds:  []string{"observer-1"},
	})
	if err != nil {
		t.Fatalf("request self-healing: %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected accepted=false for recipient mismatch")
	}
}

func TestSelfHealingE2E_VerifyHashMismatch(t *testing.T) {
	const fileKey = "key-verify-mismatch"
	payload := []byte("hello-self-healing")

	observerP2P := newFakeP2P()
	observerP2P.local[fileKey] = payload

	obsConn, obsCleanup := startSelfHealingTestServer(t, "observer-1", observerP2P, nil, nil)
	defer obsCleanup()

	obsClient := supernode.NewSelfHealingServiceClient(obsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := obsClient.VerifySelfHealing(ctx, &supernode.VerifySelfHealingRequest{
		ChallengeId:          "ch-mismatch",
		EpochId:              12,
		FileKey:              fileKey,
		RecipientId:          "recipient-1",
		ReconstructedHashHex: "deadbeef",
		ObserverId:           "observer-1",
	})
	if err != nil {
		t.Fatalf("verify self-healing: %v", err)
	}
	if ver.Ok {
		t.Fatalf("expected verify ok=false on hash mismatch")
	}
}

func TestSelfHealingE2E_VerifyFallbackReconstructWithoutPersist(t *testing.T) {
	const fileKey = "key-verify-fallback"
	const actionID = "action-verify-fallback"
	payload := []byte("fallback-self-healing")
	payloadHashHex := blake3Hex(payload)

	observerP2P := newFakeP2P()
	lumeraClient, lumeraCleanup := mockLumeraActionLookup(t, fileKey, actionID)
	defer lumeraCleanup()

	fallbackReconstructCalled := false
	factory := &fakeCascadeFactory{
		task: &fakeCascadeTask{
			recoveryFn: func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
				if req == nil || req.ActionID != actionID {
					return nil, fmt.Errorf("unexpected recovery action_id")
				}
				if req.PersistArtifacts == nil || *req.PersistArtifacts {
					return nil, fmt.Errorf("expected non-persist recovery path")
				}
				fallbackReconstructCalled = true
				return &cascadeService.RecoveryReseedResult{
					ActionID:             actionID,
					ReconstructedHashHex: payloadHashHex,
				}, nil
			},
		},
	}

	obsConn, obsCleanup := startSelfHealingTestServer(t, "observer-1", observerP2P, lumeraClient, factory)
	defer obsCleanup()
	obsClient := supernode.NewSelfHealingServiceClient(obsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := obsClient.VerifySelfHealing(ctx, &supernode.VerifySelfHealingRequest{
		ChallengeId:          "ch-fallback",
		EpochId:              12,
		FileKey:              fileKey,
		RecipientId:          "recipient-1",
		ReconstructedHashHex: payloadHashHex,
		ObserverId:           "observer-1",
		ActionId:             actionID,
	})
	if err != nil {
		t.Fatalf("verify self-healing: %v", err)
	}
	if !ver.Ok {
		t.Fatalf("expected verify ok=true, got false err=%s", ver.Error)
	}
	if !fallbackReconstructCalled {
		t.Fatalf("expected fallback reconstruction to be called")
	}
	if got := observerP2P.local[fileKey]; len(got) > 0 {
		t.Fatalf("fallback verify must not persist local artifacts")
	}
}

func TestSelfHealingE2E_VerifyObserverMismatch(t *testing.T) {
	const fileKey = "key-verify-observer-mismatch"
	payload := []byte("hello-self-healing")

	observerP2P := newFakeP2P()
	observerP2P.local[fileKey] = payload

	obsConn, obsCleanup := startSelfHealingTestServer(t, "observer-1", observerP2P, nil, nil)
	defer obsCleanup()

	obsClient := supernode.NewSelfHealingServiceClient(obsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := obsClient.VerifySelfHealing(ctx, &supernode.VerifySelfHealingRequest{
		ChallengeId:          "ch-observer-mismatch",
		EpochId:              12,
		FileKey:              fileKey,
		RecipientId:          "recipient-1",
		ReconstructedHashHex: "deadbeef",
		ObserverId:           "observer-2",
	})
	if err != nil {
		t.Fatalf("verify self-healing: %v", err)
	}
	if ver.Ok {
		t.Fatalf("expected verify ok=false for observer mismatch")
	}
}

func blake3Hex(data []byte) string {
	sum := blake3.Sum256(data)
	return hex.EncodeToString(sum[:])
}
