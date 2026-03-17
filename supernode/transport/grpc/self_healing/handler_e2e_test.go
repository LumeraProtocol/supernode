package self_healing

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
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
func (f *fakeP2P) Delete(ctx context.Context, key string) error { return nil }
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

func startSelfHealingTestServer(t *testing.T, identity string, p2p *fakeP2P) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()
	supernode.RegisterSelfHealingServiceServer(s, NewServer(identity, p2p, nil, nil))
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
	payload := []byte("hello-self-healing")

	recipientP2P := newFakeP2P()
	recipientP2P.network[fileKey] = payload // recipient missing local, recoverable from network

	observerP2P := newFakeP2P()
	observerP2P.local[fileKey] = payload // observer has local authoritative copy

	recConn, recCleanup := startSelfHealingTestServer(t, "recipient-1", recipientP2P)
	defer recCleanup()
	obsConn, obsCleanup := startSelfHealingTestServer(t, "observer-1", observerP2P)
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
	if got := recipientP2P.local[fileKey]; string(got) != string(payload) {
		t.Fatalf("recipient local store not repaired")
	}

	ver, err := obsClient.VerifySelfHealing(ctx, &supernode.VerifySelfHealingRequest{
		ChallengeId:          "ch-1",
		EpochId:              12,
		FileKey:              fileKey,
		RecipientId:          "recipient-1",
		ReconstructedHashHex: resp.ReconstructedHashHex,
		ObserverId:           "observer-1",
	})
	if err != nil {
		t.Fatalf("verify self-healing: %v", err)
	}
	if !ver.Ok {
		t.Fatalf("expected verify ok=true, got false err=%s", ver.Error)
	}
}
