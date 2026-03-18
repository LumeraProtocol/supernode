package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	grpcclient "github.com/LumeraProtocol/supernode/v2/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	snconfig "github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	shNode0KeyName = "testkey1"
	shNode1KeyName = "testkey2"
	shNode2KeyName = "testkey3"

	shNode0Identity = "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4"
	shNode1Identity = "lumera1cf0ms9ttgdvz6zwlqfty4tjcawhuaq69p40w0c"
	shNode2Identity = "lumera1cjyc4ruq739e2lakuhargejjkr0q5vg6x3d7kp"

	shUserKeyName  = "user-sh"
	shUserMnemonic = "little tone alley oval festival gloom sting asthma crime select swap auto when trip luxury pact risk sister pencil about crisp upon opera timber"
)

type selfHealingNode struct {
	KeyName  string
	Identity string
	GRPCAddr string
	HasLocal bool
}

type selfHealingRPCClient struct {
	client *grpcclient.Client
	opts   *grpcclient.ClientOptions
}

func TestSelfHealingE2EHappyPath(t *testing.T) {
	os.Setenv("INTEGRATION_TEST", "true")
	os.Setenv("INTEGRATION_TEST_ENV", "true")
	defer os.Unsetenv("INTEGRATION_TEST")
	defer os.Unsetenv("INTEGRATION_TEST_ENV")

	sut.ModifyGenesisJSON(t, SetStakingBondDenomUlume(t), SetActionParams(t), SetSupernodeMetricsParams(t))
	sut.StartChain(t)

	cli := NewLumeradCLI(t, sut, true)
	registerSelfHealingSupernodes(t, cli)

	// Keep supernodes above eligibility floor so CASCADE registration can finalize deterministically.
	cli.FundAddress(shNode0Identity, "2000000ulume")
	cli.FundAddressWithNode(shNode1Identity, "2000000ulume", "node1")
	cli.FundAddressWithNode(shNode2Identity, "2000000ulume", "node2")

	cmds := StartAllSupernodes(t)
	defer StopAllSupernodes(cmds)
	require.NoError(t, waitForSupernodeGatewaysReady(30*time.Second), "supernode gateways did not become ready")

	userAddress := cli.AddKeyFromSeed(shUserKeyName, shUserMnemonic)
	cli.FundAddress(userAddress, "1000000ulume")
	sut.AwaitNextBlock(t)

	memKR, err := snkeyring.InitKeyring(snconfig.KeyringConfig{
		Backend: "memory",
	})
	require.NoError(t, err)
	_, err = snkeyring.RecoverAccountFromMnemonic(memKR, shUserKeyName, shUserMnemonic)
	require.NoError(t, err)

	const lumeraGRPCAddr = "localhost:9090"
	const lumeraChainID = "testing"

	userLumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, shUserKeyName, memKR)
	require.NoError(t, err)
	userLumera, err := lumera.NewClient(context.Background(), userLumeraCfg)
	require.NoError(t, err)
	defer func() { _ = userLumera.Close() }()

	actionClient, err := action.NewClient(
		context.Background(),
		sdkconfig.Config{
			Account: sdkconfig.AccountConfig{
				KeyName: shUserKeyName,
				Keyring: memKR,
			},
			Lumera: sdkconfig.LumeraConfig{
				GRPCAddr: lumeraGRPCAddr,
				ChainID:  lumeraChainID,
			},
		},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()
	testFilePath := filepath.Join(WorkDir, "test.txt")

	cascadeMeta, price, expiration, err := actionClient.BuildCascadeMetadataFromFile(ctx, testFilePath, false, "")
	require.NoError(t, err)
	startSig, err := actionClient.GenerateStartCascadeSignatureFromFile(ctx, testFilePath)
	require.NoError(t, err)

	metadataBz, err := json.Marshal(cascadeMeta)
	require.NoError(t, err)

	fixtureStat, err := os.Stat(testFilePath)
	require.NoError(t, err)
	fileSizeKbs := int64(0)
	if fixtureStat.Size() > 0 {
		fileSizeKbs = (fixtureStat.Size() + 1023) / 1024
	}

	reqResp, err := userLumera.ActionMsg().RequestAction(
		ctx,
		"CASCADE",
		string(metadataBz),
		price,
		expiration,
		strconv.FormatInt(fileSizeKbs, 10),
	)
	require.NoError(t, err)
	require.NotNil(t, reqResp)
	require.NotNil(t, reqResp.TxResponse)
	require.Zero(t, reqResp.TxResponse.Code)

	txHash := reqResp.TxResponse.TxHash
	require.NotEmpty(t, txHash)
	sut.AwaitNextBlock(t)

	txResp := cli.CustomQuery("q", "tx", txHash)
	actionID := extractActionIDFromTxQuery(txResp)
	require.NotEmpty(t, actionID)

	_, err = actionClient.StartCascade(ctx, testFilePath, actionID, startSig)
	require.NoError(t, err)
	require.NoError(t, waitForActionFinalizedStateWithClient(ctx, userLumera, actionID))

	actionResp, err := userLumera.Action().GetAction(ctx, actionID)
	require.NoError(t, err)
	require.NotNil(t, actionResp)
	require.NotNil(t, actionResp.Action)
	cmeta, err := cascadekit.UnmarshalCascadeMetadata(actionResp.Action.Metadata)
	require.NoError(t, err)
	fileKey := pickAnchorKey(cmeta.RqIdsIds)
	require.NotEmpty(t, fileKey)

	node0DiskKR, err := snkeyring.InitKeyring(snconfig.KeyringConfig{
		Backend: "test",
		Dir:     filepath.Join(WorkDir, "supernode-data1", "keys"),
	})
	require.NoError(t, err)

	node0LumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, shNode0KeyName, node0DiskKR)
	require.NoError(t, err)
	node0Lumera, err := lumera.NewClient(context.Background(), node0LumeraCfg)
	require.NoError(t, err)
	defer func() { _ = node0Lumera.Close() }()

	shClient, err := newSelfHealingRPCClient(node0Lumera, node0DiskKR, shNode0Identity)
	require.NoError(t, err)

	nodes := []selfHealingNode{
		{KeyName: shNode0KeyName, Identity: shNode0Identity},
		{KeyName: shNode1KeyName, Identity: shNode1Identity},
		{KeyName: shNode2KeyName, Identity: shNode2Identity},
	}
	for i := range nodes {
		nodes[i].GRPCAddr = mustGetSupernodeLatestAddr(t, userLumera, nodes[i].Identity)
		nodes[i].HasLocal = nodeHasLocalCopy(t, shClient, nodes[i], fileKey)
	}

	var recipient selfHealingNode
	recipientForcedReconstruct := false
	for _, n := range nodes {
		if !n.HasLocal {
			recipient = n
			recipientForcedReconstruct = true
			break
		}
	}
	if recipient.Identity == "" {
		recipient = nodes[1]
	}

	observer := recipient
	for _, n := range nodes {
		if n.Identity != recipient.Identity && n.HasLocal {
			observer = n
			break
		}
	}

	challengeID := fmt.Sprintf("sh-e2e-happy-%d", time.Now().UnixNano())
	req := &pb.RequestSelfHealingRequest{
		ChallengeId:  challengeID,
		EpochId:      0,
		FileKey:      fileKey,
		ChallengerId: shNode0Identity,
		RecipientId:  recipient.Identity,
		ObserverIds:  []string{observer.Identity},
	}
	reqCtx, cancelReq := context.WithTimeout(ctx, 30*time.Second)
	defer cancelReq()
	reqRespSH, err := shClient.Request(reqCtx, recipient.Identity, recipient.GRPCAddr, req)
	require.NoError(t, err)
	require.True(t, reqRespSH.Accepted, "self-healing request was rejected: %s", reqRespSH.Error)
	require.NotEmpty(t, reqRespSH.ReconstructedHashHex)
	if recipientForcedReconstruct {
		require.True(t, reqRespSH.ReconstructionRequired, "expected reconstruction_required=true when recipient initially lacked local key")
	}

	verifyReq := &pb.VerifySelfHealingRequest{
		ChallengeId:          challengeID,
		EpochId:              0,
		FileKey:              fileKey,
		RecipientId:          recipient.Identity,
		ReconstructedHashHex: reqRespSH.ReconstructedHashHex,
		ObserverId:           observer.Identity,
	}
	verifyCtx, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
	defer cancelVerify()
	verifyResp, err := shClient.Verify(verifyCtx, observer.Identity, observer.GRPCAddr, verifyReq)
	require.NoError(t, err)
	require.True(t, verifyResp.Ok, "observer verification failed: %s", verifyResp.Error)
}

func registerSelfHealingSupernodes(t *testing.T, cli *LumeradCli) {
	t.Helper()
	type reg struct {
		nodeKey  string
		grpcPort string
		address  string
		p2pPort  string
	}
	nodes := []reg{
		{nodeKey: "node0", grpcPort: "4444", address: shNode0Identity, p2pPort: "4445"},
		{nodeKey: "node1", grpcPort: "4446", address: shNode1Identity, p2pPort: "4447"},
		{nodeKey: "node2", grpcPort: "4448", address: shNode2Identity, p2pPort: "4449"},
	}
	for _, n := range nodes {
		valAddr := strings.TrimSpace(cli.Keys("keys", "show", n.nodeKey, "--bech", "val", "-a"))
		require.NotEmpty(t, valAddr)
		resp := cli.CustomCommand(
			"tx", "supernode", "register-supernode",
			valAddr,
			"localhost:"+n.grpcPort,
			n.address,
			"--p2p-port", n.p2pPort,
			"--from", n.nodeKey,
		)
		RequireTxSuccess(t, resp)
		sut.AwaitNextBlock(t)
	}
}

func waitForActionFinalizedStateWithClient(ctx context.Context, client lumera.Client, actionID string) error {
	for i := 0; i < actionStateRetries; i++ {
		resp, err := client.Action().GetAction(ctx, actionID)
		if err == nil && resp != nil && resp.Action != nil {
			if resp.Action.State == actiontypes.ActionStateDone || resp.Action.State == actiontypes.ActionStateApproved {
				return nil
			}
		}
		time.Sleep(actionStateDelay)
	}
	return fmt.Errorf("action %s did not reach a finalized state (%s/%s)", actionID, actiontypes.ActionStateDone.String(), actiontypes.ActionStateApproved.String())
}

func extractActionIDFromTxQuery(txResp string) string {
	events := gjson.Get(txResp, "events").Array()
	for _, event := range events {
		if event.Get("type").String() != "action_registered" {
			continue
		}
		attrs := event.Get("attributes").Array()
		for _, attr := range attrs {
			if attr.Get("key").String() == "action_id" {
				return attr.Get("value").String()
			}
		}
	}
	return ""
}

func mustGetSupernodeLatestAddr(t *testing.T, client lumera.Client, identity string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := client.SuperNode().GetSupernodeWithLatestAddress(ctx, identity)
	require.NoError(t, err)
	require.NotNil(t, info)
	addr := strings.TrimSpace(info.LatestAddress)
	require.NotEmpty(t, addr)
	return addr
}

func nodeHasLocalCopy(t *testing.T, shClient *selfHealingRPCClient, node selfHealingNode, fileKey string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	probeReq := &pb.VerifySelfHealingRequest{
		ChallengeId:          fmt.Sprintf("sh-probe-%s-%d", node.KeyName, time.Now().UnixNano()),
		EpochId:              0,
		FileKey:              fileKey,
		RecipientId:          "",
		ReconstructedHashHex: strings.Repeat("0", 64),
		ObserverId:           node.Identity,
	}
	resp, err := shClient.Verify(ctx, node.Identity, node.GRPCAddr, probeReq)
	require.NoError(t, err)
	if resp == nil {
		return false
	}
	if strings.Contains(strings.ToLower(resp.Error), "missing local file") {
		return false
	}
	return true
}

func pickAnchorKey(keys []string) string {
	anchor := ""
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if anchor == "" || key < anchor {
			anchor = key
		}
	}
	return anchor
}

func waitForSupernodeGatewaysReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	urls := []string{
		"http://localhost:8002/api/v1/status",
		"http://localhost:8003/api/v1/status",
		"http://localhost:8004/api/v1/status",
	}

	for time.Now().Before(deadline) {
		allReady := true
		for _, u := range urls {
			resp, err := http.Get(u) //nolint:gosec
			if err != nil {
				allReady = false
				break
			}
			body, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if rerr != nil || resp.StatusCode >= 400 || len(body) == 0 {
				allReady = false
				break
			}
			if !gjson.ValidBytes(body) {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timed out waiting for supernode gateways readiness")
}

func newSelfHealingRPCClient(lumeraClient lumera.Client, kr keyring.Keyring, localIdentity string) (*selfHealingRPCClient, error) {
	validator := lumera.NewSecureKeyExchangeValidator(lumeraClient)
	grpcCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       kr,
			LocalIdentity: localIdentity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	if err != nil {
		return nil, err
	}
	opts := grpcclient.DefaultClientOptions()
	opts.EnableRetries = true
	return &selfHealingRPCClient{
		client: grpcclient.NewClient(grpcCreds),
		opts:   opts,
	}, nil
}

func (c *selfHealingRPCClient) Request(ctx context.Context, remoteIdentity string, address string, req *pb.RequestSelfHealingRequest) (*pb.RequestSelfHealingResponse, error) {
	conn, err := c.client.Connect(ctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), c.opts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewSelfHealingServiceClient(conn).RequestSelfHealing(ctx, req)
}

func (c *selfHealingRPCClient) Verify(ctx context.Context, remoteIdentity string, address string, req *pb.VerifySelfHealingRequest) (*pb.VerifySelfHealingResponse, error) {
	conn, err := c.client.Connect(ctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), c.opts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewSelfHealingServiceClient(conn).VerifySelfHealing(ctx, req)
}
