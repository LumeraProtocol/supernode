package supernode_metrics

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"testing"
	"time"

	tmp2p "github.com/cometbft/cometbft/proto/tendermint/p2p"
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

func TestBuildProbeCandidatesFilters(t *testing.T) {
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

	senders, receivers, peersByID := buildProbeCandidates(active)
	if len(senders) != 4 {
		t.Fatalf("expected 4 senders (ACTIVE only), got %d", len(senders))
	}
	if len(receivers) != 5 {
		t.Fatalf("expected 5 receivers (ACTIVE+POSTPONED), got %d", len(receivers))
	}
	if peersByID["a"].grpcPort != 4444 || peersByID["a"].p2pPort != 5555 || peersByID["a"].metricsHeight != height {
		t.Fatalf("unexpected peer a: %+v", peersByID["a"])
	}
	if peersByID["postponed"].identity == "" {
		t.Fatalf("expected postponed peer present")
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

	store := reachability.NewStore()
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

	store := reachability.NewStore()
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

	store := reachability.NewStore()
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

	peersByID := map[string]probeTarget{
		"a": {identity: "a", metricsHeight: height},
		"b": {identity: "b", metricsHeight: height},
		"c": {identity: "c", metricsHeight: height},
		"d": {identity: "d", metricsHeight: height},
		"e": {identity: "e", metricsHeight: height},
	}
	hm.setProbePlan(&probePlan{
		epochID:         2, // floor(250/100)
		epochBlocks:     epochBlocks,
		peersByID:       peersByID,
		senders:         []probeTarget{peersByID["a"], peersByID["b"], peersByID["c"], peersByID["d"], peersByID["e"]},
		receivers:       []probeTarget{peersByID["a"], peersByID["b"], peersByID["c"], peersByID["d"], peersByID["e"]},
		expectedInbound: map[string]int{"c": 2},
		assignedProbers: map[string][]string{"c": {"a", "b"}},
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

func TestRefreshProbePlanStoresSnapshotForNonSenderReceiver(t *testing.T) {
	const (
		height      = int64(250)
		epochBlocks = uint64(100)
	)

	peers := []*sntypes.SuperNode{
		mkSNState("a", "203.0.113.1", height, height, sntypes.SuperNodeStateActive),
		mkSNState("b", "203.0.113.2", height, height, sntypes.SuperNodeStateActive),
		mkSNState("c", "203.0.113.3", height, height, sntypes.SuperNodeStatePostponed), // self is receiver-only
		mkSNState("d", "203.0.113.4", height, height, sntypes.SuperNodeStateActive),
	}
	client := &fakeLumeraClient{
		nodeModule: &fakeNodeModule{height: height},
		snModule:   &fakeSupernodeModule{supernodes: peers},
	}

	hm := &Collector{
		lumeraClient:                client,
		identity:                    "c",
		metricsUpdateIntervalBlocks: epochBlocks,
		metricsFreshnessMaxBlocks:   5000,
	}

	plan, reset := hm.refreshProbePlanIfNeeded(context.Background(), nil, "c")
	if !reset {
		t.Fatalf("expected plan refresh reset")
	}
	if plan == nil {
		t.Fatalf("expected non-nil plan for receiver-only node")
	}
	if plan.epochID != 2 {
		t.Fatalf("expected epochID=2, got %d", plan.epochID)
	}
	if len(plan.outboundTargets) != 0 {
		t.Fatalf("expected no outbound targets for non-sender, got %d", len(plan.outboundTargets))
	}

	// Ensure the snapshot is stored on the collector for quorum calculations.
	got := hm.getProbePlan()
	if got == nil {
		t.Fatalf("expected collector probe plan set")
	}
	if got.epochID != plan.epochID || got.epochBlocks != plan.epochBlocks {
		t.Fatalf("unexpected stored plan epoch: %+v vs %+v", got, plan)
	}
	if got.expectedInbound == nil || got.assignedProbers == nil || got.peersByID == nil {
		t.Fatalf("expected stored plan to include assignment maps")
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

func mkSNState(account string, ip string, recordHeight int64, metricsHeight int64, state sntypes.SuperNodeState) *sntypes.SuperNode {
	return &sntypes.SuperNode{
		SupernodeAccount: account,
		States: []*sntypes.SuperNodeStateRecord{
			{Height: recordHeight, State: state},
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
		SdkBlock: &cmtservice.Block{Header: cmtservice.Header{Height: m.height}},
	}, nil
}
func (m *fakeNodeModule) GetBlockByHeight(_ context.Context, height int64) (*cmtservice.GetBlockByHeightResponse, error) {
	h := sha256.Sum256([]byte(fmt.Sprintf("epoch-hash:%d", height)))
	return &cmtservice.GetBlockByHeightResponse{
		BlockId: &tmproto.BlockID{Hash: h[:]},
	}, nil
}
func (m *fakeNodeModule) GetNodeInfo(context.Context) (*cmtservice.GetNodeInfoResponse, error) {
	return &cmtservice.GetNodeInfoResponse{
		DefaultNodeInfo: &tmp2p.DefaultNodeInfo{Network: "chain-1"},
	}, nil
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
