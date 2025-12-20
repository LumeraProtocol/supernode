package supernode_metrics

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtservice "github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/bank"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
	"github.com/LumeraProtocol/supernode/v2/pkg/reachability"
)

func TestDeriveProbeOffsets(t *testing.T) {
	offsets := deriveProbeOffsets(123, 3, 10)
	if len(offsets) != 3 {
		t.Fatalf("expected 3 offsets, got %d", len(offsets))
	}
	seen := map[int]struct{}{}
	for _, off := range offsets {
		if off < 1 || off > 9 {
			t.Fatalf("offset out of range: %d", off)
		}
		if _, ok := seen[off]; ok {
			t.Fatalf("duplicate offset: %d", off)
		}
		seen[off] = struct{}{}
	}

	offsets2 := deriveProbeOffsets(123, 3, 10)
	for i := range offsets {
		if offsets[i] != offsets2[i] {
			t.Fatalf("offsets not deterministic: %v vs %v", offsets, offsets2)
		}
	}
}

func TestDeriveProbeOffsetsTruncatesWhenKExceedsPeers(t *testing.T) {
	offsets := deriveProbeOffsets(1, 100, 4)
	if len(offsets) != 3 {
		t.Fatalf("expected 3 offsets (N-1), got %d", len(offsets))
	}
	seen := map[int]struct{}{}
	for _, off := range offsets {
		if off < 1 || off > 3 {
			t.Fatalf("offset out of range: %d", off)
		}
		if _, ok := seen[off]; ok {
			t.Fatalf("duplicate offset: %d", off)
		}
		seen[off] = struct{}{}
	}
}

func TestDeterministicScheduleDistinctProbers(t *testing.T) {
	const (
		nPeers = 7
		epoch  = uint64(42)
		k      = 3
	)
	offsets := deriveProbeOffsets(epoch, k, nPeers)
	if len(offsets) != k {
		t.Fatalf("expected %d offsets, got %d", k, len(offsets))
	}

	for target := 0; target < nPeers; target++ {
		seen := map[int]struct{}{}
		for _, off := range offsets {
			prober := (target - off) % nPeers
			if prober < 0 {
				prober += nPeers
			}
			if _, ok := seen[prober]; ok {
				t.Fatalf("target=%d: duplicate prober=%d for offsets=%v", target, prober, offsets)
			}
			seen[prober] = struct{}{}
		}
		if len(seen) != k {
			t.Fatalf("target=%d: expected %d distinct probers, got %d", target, k, len(seen))
		}
	}
}

func TestBuildEligiblePeersFilters(t *testing.T) {
	height := int64(100)
	active := []*sntypes.SuperNode{
		{
			SupernodeAccount: "a",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStateActive},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "203.0.113.1:4444"},
			},
			Metrics: &sntypes.MetricsAggregate{Height: height},
			P2PPort: "5555",
		},
		{
			SupernodeAccount: "b",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStateActive},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "203.0.113.2"}, // no port => default APIPort
			},
			Metrics: &sntypes.MetricsAggregate{Height: 99},
		},
		{
			SupernodeAccount: "postponed",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStatePostponed},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "203.0.113.3:4444"},
			},
		},
		{
			SupernodeAccount: "ipv6",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStateActive},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "[2001:db8::1]:4444"},
			},
		},
		{
			SupernodeAccount: "private",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStateActive},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "192.168.1.10:4444"},
			},
		},
		{
			SupernodeAccount: "",
			States: []*sntypes.SuperNodeStateRecord{
				{Height: height, State: sntypes.SuperNodeStateActive},
			},
			PrevIpAddresses: []*sntypes.IPAddressHistory{
				{Height: height, Address: "203.0.113.9:4444"},
			},
		},
	}

	got := buildEligiblePeers(active, false)
	if len(got) != 2 {
		t.Fatalf("expected 2 eligible peers, got %d", len(got))
	}
	if got[0].identity != "a" || got[0].grpcPort != 4444 || got[0].p2pPort != 5555 || got[0].metricsHeight != height {
		t.Fatalf("unexpected peer a: %+v", got[0])
	}
	if got[1].identity != "b" || got[1].grpcPort != APIPort || got[1].metricsHeight != 99 {
		t.Fatalf("unexpected peer b: %+v", got[1])
	}

	gotTest := buildEligiblePeers(active, true)
	if len(gotTest) != 3 {
		t.Fatalf("expected 3 eligible peers in integration test mode, got %d", len(gotTest))
	}
	foundPrivate := false
	for _, p := range gotTest {
		if p.identity == "private" {
			foundPrivate = true
			break
		}
	}
	if !foundPrivate {
		t.Fatalf("expected private peer to be eligible in integration test mode")
	}
}

func TestOpenPortsClosedWhenQuorumSatisfiedAndNoEvidence(t *testing.T) {
	const (
		currentHeight = int64(250)
		epochBlocks   = uint64(100)
	)

	peers := []*sntypes.SuperNode{
		mkSN("a", "203.0.113.1", currentHeight, currentHeight),
		mkSN("b", "203.0.113.2", currentHeight, currentHeight),
		mkSN("c", "203.0.113.3", currentHeight, currentHeight), // self
		mkSN("d", "203.0.113.4", currentHeight, currentHeight),
		mkSN("e", "203.0.113.5", currentHeight, currentHeight),
	}

	client := &fakeLumeraClient{
		nodeModule: &fakeNodeModule{height: currentHeight},
		snModule:   &fakeSupernodeModule{supernodes: peers},
	}

	store := reachability.NewStore("c")
	reachability.SetDefaultStore(store)
	t.Cleanup(func() { reachability.SetDefaultStore(nil) })

	hm := &Collector{
		lumeraClient:                client,
		identity:                    "c",
		metricsUpdateIntervalBlocks: epochBlocks,
		metricsFreshnessMaxBlocks:   5000,
		grpcPort:                    APIPort,
		p2pPort:                     P2PPort,
		gatewayPort:                 StatusPort,
	}

	ports := hm.openPorts(context.Background())
	if len(ports) != 3 {
		t.Fatalf("expected 3 port statuses, got %d", len(ports))
	}
	for _, ps := range ports {
		if ps.State != sntypes.PortState_PORT_STATE_CLOSED {
			t.Fatalf("expected CLOSED, got %v for port=%d", ps.State, ps.Port)
		}
	}
}

func TestOpenPortsUnknownWithoutQuorum(t *testing.T) {
	// Only two eligible peers => cannot meet ProbeQuorum=2 (needs at least 3 peers).
	peers := []*sntypes.SuperNode{
		mkSN("a", "203.0.113.1", 250, 250),
		mkSN("b", "203.0.113.2", 250, 250), // self
	}

	client := &fakeLumeraClient{
		nodeModule: &fakeNodeModule{height: 250},
		snModule:   &fakeSupernodeModule{supernodes: peers},
	}

	store := reachability.NewStore("b")
	reachability.SetDefaultStore(store)
	t.Cleanup(func() { reachability.SetDefaultStore(nil) })

	hm := &Collector{
		lumeraClient:                client,
		identity:                    "b",
		metricsUpdateIntervalBlocks: 100,
		metricsFreshnessMaxBlocks:   5000,
		grpcPort:                    APIPort,
		p2pPort:                     P2PPort,
		gatewayPort:                 StatusPort,
	}

	ports := hm.openPorts(context.Background())
	if len(ports) != 3 {
		t.Fatalf("expected 3 port statuses, got %d", len(ports))
	}
	for _, ps := range ports {
		if ps.State != sntypes.PortState_PORT_STATE_UNKNOWN {
			t.Fatalf("expected UNKNOWN, got %v for port=%d", ps.State, ps.Port)
		}
	}
}

func TestOpenPortsUnknownWhenStoreNil(t *testing.T) {
	const height = int64(250)
	peers := []*sntypes.SuperNode{
		mkSN("a", "203.0.113.1", height, height),
		mkSN("b", "203.0.113.2", height, height),
		mkSN("c", "203.0.113.3", height, height), // self
		mkSN("d", "203.0.113.4", height, height),
		mkSN("e", "203.0.113.5", height, height),
	}

	client := &fakeLumeraClient{
		nodeModule: &fakeNodeModule{height: height},
		snModule:   &fakeSupernodeModule{supernodes: peers},
	}

	reachability.SetDefaultStore(nil)

	hm := &Collector{
		lumeraClient:                client,
		identity:                    "c",
		metricsUpdateIntervalBlocks: 100,
		metricsFreshnessMaxBlocks:   5000,
		grpcPort:                    APIPort,
		p2pPort:                     P2PPort,
		gatewayPort:                 StatusPort,
	}

	ports := hm.openPorts(context.Background())
	if len(ports) != 3 {
		t.Fatalf("expected 3 port statuses, got %d", len(ports))
	}
	for _, ps := range ports {
		if ps.State != sntypes.PortState_PORT_STATE_UNKNOWN {
			t.Fatalf("expected UNKNOWN, got %v for port=%d", ps.State, ps.Port)
		}
	}
}

func TestOpenPortsOpenOverridesClosed(t *testing.T) {
	const height = int64(250)
	peers := []*sntypes.SuperNode{
		mkSN("a", "203.0.113.1", height, height),
		mkSN("b", "203.0.113.2", height, height),
		mkSN("c", "203.0.113.3", height, height), // self
		mkSN("d", "203.0.113.4", height, height),
		mkSN("e", "203.0.113.5", height, height),
	}

	client := &fakeLumeraClient{
		nodeModule: &fakeNodeModule{height: height},
		snModule:   &fakeSupernodeModule{supernodes: peers},
	}

	store := reachability.NewStore("c")
	reachability.SetDefaultStore(store)
	t.Cleanup(func() { reachability.SetDefaultStore(nil) })

	store.RecordInbound(reachability.ServiceGRPC, "a", &net.TCPAddr{IP: net.ParseIP("203.0.113.50"), Port: 1234}, time.Now())

	hm := &Collector{
		lumeraClient:                client,
		identity:                    "c",
		metricsUpdateIntervalBlocks: 100,
		metricsFreshnessMaxBlocks:   5000,
		grpcPort:                    APIPort,
		p2pPort:                     P2PPort,
		gatewayPort:                 StatusPort,
	}

	ports := hm.openPorts(context.Background())
	for _, ps := range ports {
		switch ps.Port {
		case APIPort:
			if ps.State != sntypes.PortState_PORT_STATE_OPEN {
				t.Fatalf("expected gRPC OPEN, got %v", ps.State)
			}
		case P2PPort, StatusPort:
			if ps.State != sntypes.PortState_PORT_STATE_CLOSED {
				t.Fatalf("expected port=%d CLOSED, got %v", ps.Port, ps.State)
			}
		default:
			t.Fatalf("unexpected port: %d", ps.Port)
		}
	}
}

func TestSilenceImpliesClosedUsesProbePlanSnapshot(t *testing.T) {
	const (
		height      = int64(250)
		epochBlocks = uint64(100)
	)

	hm := &Collector{
		lumeraClient:                &fakeLumeraClient{nodeModule: &fakeNodeModule{height: height}},
		identity:                    "c",
		metricsUpdateIntervalBlocks: epochBlocks,
		metricsFreshnessMaxBlocks:   5000,
	}

	hm.setProbePlan(&probePlan{
		epochID:     2, // floor(250/100)
		epochBlocks: epochBlocks,
		eligiblePeers: []probeTarget{
			{identity: "a", metricsHeight: height},
			{identity: "b", metricsHeight: height},
			{identity: "c", metricsHeight: height},
			{identity: "d", metricsHeight: height},
			{identity: "e", metricsHeight: height},
		},
	})

	if !hm.silenceImpliesClosed(context.Background()) {
		t.Fatalf("expected quorum to be satisfied using the cached probe plan")
	}
}

func TestProbeRoundIntervalEnforcesMinAttemptsPerReportInterval(t *testing.T) {
	if got := probeRoundInterval(30*time.Second, 1); got != 10*time.Second {
		t.Fatalf("expected 30s/3=10s for 1 target, got %v", got)
	}
	if got := probeRoundInterval(30*time.Second, 2); got != 10*time.Second {
		t.Fatalf("expected 30s/3=10s for 2 targets, got %v", got)
	}
	if got := probeRoundInterval(30*time.Second, 3); got != 10*time.Second {
		t.Fatalf("expected 30s/3=10s for 3 targets, got %v", got)
	}
	if got := probeRoundInterval(30*time.Second, 6); got != 5*time.Second {
		t.Fatalf("expected 30s/6=5s for 6 targets, got %v", got)
	}
}

func mkSN(account string, ip string, recordHeight int64, metricsHeight int64) *sntypes.SuperNode {
	return &sntypes.SuperNode{
		SupernodeAccount: account,
		States: []*sntypes.SuperNodeStateRecord{
			{Height: recordHeight, State: sntypes.SuperNodeStateActive},
		},
		PrevIpAddresses: []*sntypes.IPAddressHistory{
			{Height: recordHeight, Address: fmt.Sprintf("%s:%d", ip, APIPort)},
		},
		Metrics: &sntypes.MetricsAggregate{Height: metricsHeight},
		P2PPort: fmt.Sprintf("%d", P2PPort),
	}
}

type fakeLumeraClient struct {
	nodeModule node.Module
	snModule   supernode.Module
}

func (c *fakeLumeraClient) Auth() auth.Module                  { return nil }
func (c *fakeLumeraClient) Action() action.Module              { return nil }
func (c *fakeLumeraClient) ActionMsg() action_msg.Module       { return nil }
func (c *fakeLumeraClient) SuperNode() supernode.Module        { return c.snModule }
func (c *fakeLumeraClient) SuperNodeMsg() supernode_msg.Module { return nil }
func (c *fakeLumeraClient) Bank() bank.Module                  { return nil }
func (c *fakeLumeraClient) Tx() tx.Module                      { return nil }
func (c *fakeLumeraClient) Node() node.Module                  { return c.nodeModule }
func (c *fakeLumeraClient) Close() error                       { return nil }

type fakeNodeModule struct {
	height int64
}

func (m *fakeNodeModule) GetLatestBlock(ctx context.Context) (*cmtservice.GetLatestBlockResponse, error) {
	_ = ctx
	return &cmtservice.GetLatestBlockResponse{
		Block: &tmproto.Block{Header: tmproto.Header{Height: m.height}},
	}, nil
}
func (m *fakeNodeModule) GetBlockByHeight(context.Context, int64) (*cmtservice.GetBlockByHeightResponse, error) {
	return nil, nil
}
func (m *fakeNodeModule) GetNodeInfo(context.Context) (*cmtservice.GetNodeInfoResponse, error) {
	return nil, nil
}
func (m *fakeNodeModule) GetSyncing(context.Context) (*cmtservice.GetSyncingResponse, error) {
	return nil, nil
}
func (m *fakeNodeModule) GetLatestValidatorSet(context.Context) (*cmtservice.GetLatestValidatorSetResponse, error) {
	return nil, nil
}
func (m *fakeNodeModule) GetValidatorSetByHeight(context.Context, int64) (*cmtservice.GetValidatorSetByHeightResponse, error) {
	return nil, nil
}
func (m *fakeNodeModule) Sign(string, []byte) ([]byte, error) { return nil, nil }

type fakeSupernodeModule struct {
	supernodes []*sntypes.SuperNode
}

func (m *fakeSupernodeModule) GetTopSuperNodesForBlock(context.Context, *sntypes.QueryGetTopSuperNodesForBlockRequest) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return nil, nil
}
func (m *fakeSupernodeModule) GetSuperNode(context.Context, string) (*sntypes.QueryGetSuperNodeResponse, error) {
	return nil, nil
}
func (m *fakeSupernodeModule) GetSupernodeBySupernodeAddress(context.Context, string) (*sntypes.SuperNode, error) {
	return nil, nil
}
func (m *fakeSupernodeModule) GetSupernodeWithLatestAddress(context.Context, string) (*supernode.SuperNodeInfo, error) {
	return nil, nil
}
func (m *fakeSupernodeModule) GetParams(context.Context) (*sntypes.QueryParamsResponse, error) {
	return nil, nil
}
func (m *fakeSupernodeModule) ListSuperNodes(context.Context) (*sntypes.QueryListSuperNodesResponse, error) {
	return &sntypes.QueryListSuperNodesResponse{Supernodes: m.supernodes}, nil
}
