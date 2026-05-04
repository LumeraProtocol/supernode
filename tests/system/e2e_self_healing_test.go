package system

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	Identity string
	GRPCAddr string
}

type selfHealingRPCClient struct {
	client *grpcclient.Client
	opts   *grpcclient.ClientOptions
}

type selfHealingFixture struct {
	cli               *LumeradCli
	userLumera        lumera.Client
	shClient          *selfHealingRPCClient
	nodes             []selfHealingNode
	recipient         selfHealingNode
	observer          selfHealingNode
	actionID          string
	fileKey           string
	registeredHashHex string
	cmds              []*exec.Cmd
}

func TestSelfHealingE2EHappyPath(t *testing.T) {
	fixture := setupSelfHealingFixture(t)

	ctx := context.Background()
	challengeID := newSelfHealingChallengeID("sh-e2e-happy")

	req := buildSelfHealingRequest(challengeID, 0, fixture)
	reqCtx, cancelReq := context.WithTimeout(ctx, 30*time.Second)
	defer cancelReq()

	t.Logf("self-healing request start challenge_id=%s recipient=%s observers=%v", challengeID, fixture.recipient.Identity, req.ObserverIds)
	reqResp, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
	require.NoError(t, err)
	require.True(t, reqResp.Accepted, "self-healing request was rejected: %s", reqResp.Error)
	require.NotEmpty(t, reqResp.ReconstructedHashHex)
	t.Logf("self-healing request response accepted=%t reconstruction_required=%t reconstructed_hash=%s", reqResp.Accepted, reqResp.ReconstructionRequired, reqResp.ReconstructedHashHex)
	require.True(t, strings.EqualFold(reqResp.ReconstructedHashHex, fixture.registeredHashHex), "recipient reconstructed hash mismatch: got=%s want=%s", reqResp.ReconstructedHashHex, fixture.registeredHashHex)
	t.Logf("self-healing hash assertion passed reconstructed_hash=%s registered_action_hash=%s", reqResp.ReconstructedHashHex, fixture.registeredHashHex)

	verifyReq := buildSelfHealingVerifyRequest(challengeID, 0, reqResp.ReconstructedHashHex, fixture)
	verifyCtx, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
	defer cancelVerify()

	t.Logf("self-healing verify start challenge_id=%s observer=%s recipient=%s", challengeID, fixture.observer.Identity, fixture.recipient.Identity)
	verifyResp, err := fixture.shClient.Verify(verifyCtx, fixture.observer.Identity, fixture.observer.GRPCAddr, verifyReq)
	require.NoError(t, err)
	require.True(t, verifyResp.Ok, "observer verification failed: %s", verifyResp.Error)
	t.Logf("self-healing verify response observer=%s ok=%t error=%q", verifyResp.ObserverId, verifyResp.Ok, verifyResp.Error)

	commitReq := buildSelfHealingCommitRequest(challengeID, 0, fixture)
	commitCtx, cancelCommit := context.WithTimeout(ctx, 30*time.Second)
	defer cancelCommit()

	t.Logf("self-healing commit start challenge_id=%s recipient=%s", challengeID, fixture.recipient.Identity)
	commitResp, err := fixture.shClient.Commit(commitCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, commitReq)
	require.NoError(t, err)
	require.True(t, commitResp.Stored, "self-healing commit failed: %s", commitResp.Error)
	t.Logf("self-healing commit response stored=%t error=%q", commitResp.Stored, commitResp.Error)
}

func TestSelfHealingE2EFailureScenarios(t *testing.T) {
	fixture := setupSelfHealingFixture(t)
	ctx := context.Background()

	t.Run("ObserverRejectsTamperedHash", func(t *testing.T) {
		challengeID := newSelfHealingChallengeID("sh-e2e-verify-mismatch")
		req := buildSelfHealingRequest(challengeID, 0, fixture)

		reqCtx, cancelReq := context.WithTimeout(ctx, 30*time.Second)
		defer cancelReq()

		reqResp, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
		require.NoError(t, err)
		require.True(t, reqResp.Accepted, "self-healing request was rejected: %s", reqResp.Error)
		require.NotEmpty(t, reqResp.ReconstructedHashHex)

		tampered := tamperHexChar(reqResp.ReconstructedHashHex)
		verifyReq := buildSelfHealingVerifyRequest(challengeID, 0, tampered, fixture)

		verifyCtx, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
		defer cancelVerify()

		verifyResp, err := fixture.shClient.Verify(verifyCtx, fixture.observer.Identity, fixture.observer.GRPCAddr, verifyReq)
		require.NoError(t, err)
		require.False(t, verifyResp.Ok, "tampered hash must fail observer verification")
		require.NotEmpty(t, verifyResp.Error)
		t.Logf("self-healing tampered verify response observer=%s ok=%t error=%q", verifyResp.ObserverId, verifyResp.Ok, verifyResp.Error)
	})

	t.Run("StaleEpochRejected", func(t *testing.T) {
		epochResp, err := fixture.userLumera.Audit().GetCurrentEpoch(ctx)
		require.NoError(t, err)
		require.NotNil(t, epochResp)

		staleEpoch := epochResp.GetEpochId() + 100
		challengeID := newSelfHealingChallengeID("sh-e2e-stale")

		req := buildSelfHealingRequest(challengeID, staleEpoch, fixture)
		reqCtx, cancelReq := context.WithTimeout(ctx, 30*time.Second)
		defer cancelReq()

		reqResp, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
		require.NoError(t, err)
		require.False(t, reqResp.Accepted)
		require.Contains(t, strings.ToLower(reqResp.Error), "stale epoch")

		verifyReq := buildSelfHealingVerifyRequest(challengeID, staleEpoch, fixture.registeredHashHex, fixture)
		verifyCtx, cancelVerify := context.WithTimeout(ctx, 30*time.Second)
		defer cancelVerify()

		verifyResp, err := fixture.shClient.Verify(verifyCtx, fixture.observer.Identity, fixture.observer.GRPCAddr, verifyReq)
		require.NoError(t, err)
		require.False(t, verifyResp.Ok)
		require.Contains(t, strings.ToLower(verifyResp.Error), "stale epoch")

		commitReq := buildSelfHealingCommitRequest(challengeID, staleEpoch, fixture)
		commitCtx, cancelCommit := context.WithTimeout(ctx, 30*time.Second)
		defer cancelCommit()

		commitResp, err := fixture.shClient.Commit(commitCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, commitReq)
		require.NoError(t, err)
		require.False(t, commitResp.Stored)
		require.Contains(t, strings.ToLower(commitResp.Error), "stale epoch")
	})

	t.Run("DuplicateChallengeReplay", func(t *testing.T) {
		challengeID := newSelfHealingChallengeID("sh-e2e-duplicate")
		req := buildSelfHealingRequest(challengeID, 0, fixture)

		reqCtx, cancelReq := context.WithTimeout(ctx, 30*time.Second)
		defer cancelReq()

		firstResp, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
		require.NoError(t, err)
		require.True(t, firstResp.Accepted, "initial request rejected: %s", firstResp.Error)
		require.True(t, strings.EqualFold(firstResp.ReconstructedHashHex, fixture.registeredHashHex), "first request reconstructed hash mismatch")

		replayResp, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
		require.NoError(t, err)
		if replayResp.Accepted {
			require.True(t, strings.EqualFold(replayResp.ReconstructedHashHex, firstResp.ReconstructedHashHex), "replay reconstructed hash diverged")
			require.True(t, strings.EqualFold(replayResp.ReconstructedHashHex, fixture.registeredHashHex), "replay reconstructed hash mismatch with action hash")
		} else {
			require.NotEmpty(t, replayResp.Error, "replay rejection must include a reason")
		}
	})

	t.Run("RecipientDownRequestFails", func(t *testing.T) {
		stopProcessWithTimeout(fixture.cmds[1], 3*time.Second)
		require.NoError(t, waitForTCPUnavailable(fixture.recipient.GRPCAddr, 10*time.Second), "recipient gRPC endpoint should be down before request")

		challengeID := newSelfHealingChallengeID("sh-e2e-recipient-down")
		req := buildSelfHealingRequest(challengeID, 0, fixture)
		reqCtx, cancelReq := context.WithTimeout(ctx, 10*time.Second)
		defer cancelReq()

		_, err := fixture.shClient.Request(reqCtx, fixture.recipient.Identity, fixture.recipient.GRPCAddr, req)
		require.Error(t, err, "request should fail while recipient is down")
		t.Logf("self-healing recipient down request error=%v", err)
	})
}

func setupSelfHealingFixture(t *testing.T) *selfHealingFixture {
	t.Helper()

	os.Setenv("INTEGRATION_TEST", "true")
	os.Setenv("INTEGRATION_TEST_ENV", "true")
	t.Cleanup(func() {
		os.Unsetenv("INTEGRATION_TEST")
		os.Unsetenv("INTEGRATION_TEST_ENV")
	})

	sut.ModifyGenesisJSON(t, SetStakingBondDenomUlume(t), SetActionParams(t), SetSupernodeMetricsParams(t))
	sut.StartChain(t)

	cli := NewLumeradCLI(t, sut, true)
	registerSelfHealingSupernodes(t, cli)

	// Keep supernodes above eligibility floor so CASCADE registration can finalize deterministically.
	cli.FundAddress(shNode0Identity, "2000000ulume")
	cli.FundAddressWithNode(shNode1Identity, "2000000ulume", "node1")
	cli.FundAddressWithNode(shNode2Identity, "2000000ulume", "node2")

	cmds := StartAllSupernodes(t)
	t.Cleanup(func() {
		StopAllSupernodes(cmds)
	})

	require.NoError(t, waitForSupernodeGatewaysReady(30*time.Second), "supernode gateways did not become ready")

	userAddress := cli.AddKeyFromSeed(shUserKeyName, shUserMnemonic)
	cli.FundAddress(userAddress, "1000000ulume")
	sut.AwaitNextBlock(t)

	memKR, err := snkeyring.InitKeyring(snconfig.KeyringConfig{Backend: "memory"})
	require.NoError(t, err)
	_, err = snkeyring.RecoverAccountFromMnemonic(memKR, shUserKeyName, shUserMnemonic)
	require.NoError(t, err)

	const lumeraGRPCAddr = "localhost:9090"
	const lumeraChainID = "testing"

	userLumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, shUserKeyName, memKR)
	require.NoError(t, err)
	userLumera, err := lumera.NewClient(context.Background(), userLumeraCfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = userLumera.Close()
	})

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
	registeredHashRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cmeta.DataHash))
	require.NoError(t, err)
	require.NotEmpty(t, registeredHashRaw)

	registeredHashHex := hex.EncodeToString(registeredHashRaw)
	fileKey := pickAnchorKey(cmeta.RqIdsIds)
	require.NotEmpty(t, fileKey)
	t.Logf("self-healing fixture prepared action_id=%s file_key=%s registered_action_hash=%s", actionID, fileKey, registeredHashHex)

	node0DiskKR, err := snkeyring.InitKeyring(snconfig.KeyringConfig{
		Backend: "test",
		Dir:     filepath.Join(WorkDir, "supernode-data1", "keys"),
	})
	require.NoError(t, err)

	node0LumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, shNode0KeyName, node0DiskKR)
	require.NoError(t, err)
	node0Lumera, err := lumera.NewClient(context.Background(), node0LumeraCfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = node0Lumera.Close()
	})

	shClient, err := newSelfHealingRPCClient(node0Lumera, node0DiskKR, shNode0Identity)
	require.NoError(t, err)

	nodes := []selfHealingNode{
		{Identity: shNode0Identity},
		{Identity: shNode1Identity},
		{Identity: shNode2Identity},
	}
	for i := range nodes {
		nodes[i].GRPCAddr = mustGetSupernodeLatestAddr(t, userLumera, nodes[i].Identity)
	}

	// Keep roles deterministic and distinct in system tests.
	recipient := nodes[1]
	observer := nodes[2]
	t.Logf("self-healing role selection recipient=%s observer=%s", recipient.Identity, observer.Identity)

	return &selfHealingFixture{
		cli:               cli,
		userLumera:        userLumera,
		shClient:          shClient,
		nodes:             nodes,
		recipient:         recipient,
		observer:          observer,
		actionID:          actionID,
		fileKey:           fileKey,
		registeredHashHex: registeredHashHex,
		cmds:              cmds,
	}
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

func newSelfHealingChallengeID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func buildSelfHealingRequest(challengeID string, epochID uint64, f *selfHealingFixture) *pb.RequestSelfHealingRequest {
	return &pb.RequestSelfHealingRequest{
		ChallengeId:  challengeID,
		EpochId:      epochID,
		FileKey:      f.fileKey,
		ChallengerId: shNode0Identity,
		RecipientId:  f.recipient.Identity,
		ObserverIds:  []string{f.observer.Identity},
		ActionId:     f.actionID,
	}
}

func buildSelfHealingVerifyRequest(challengeID string, epochID uint64, reconstructedHashHex string, f *selfHealingFixture) *pb.VerifySelfHealingRequest {
	return &pb.VerifySelfHealingRequest{
		ChallengeId:          challengeID,
		EpochId:              epochID,
		FileKey:              f.fileKey,
		RecipientId:          f.recipient.Identity,
		ReconstructedHashHex: reconstructedHashHex,
		ObserverId:           f.observer.Identity,
		ActionId:             f.actionID,
	}
}

func buildSelfHealingCommitRequest(challengeID string, epochID uint64, f *selfHealingFixture) *pb.CommitSelfHealingRequest {
	return &pb.CommitSelfHealingRequest{
		ChallengeId:  challengeID,
		EpochId:      epochID,
		FileKey:      f.fileKey,
		ActionId:     f.actionID,
		ChallengerId: shNode0Identity,
		RecipientId:  f.recipient.Identity,
	}
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
	return &selfHealingRPCClient{client: grpcclient.NewClient(grpcCreds), opts: opts}, nil
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

func (c *selfHealingRPCClient) Commit(ctx context.Context, remoteIdentity string, address string, req *pb.CommitSelfHealingRequest) (*pb.CommitSelfHealingResponse, error) {
	conn, err := c.client.Connect(ctx, fmt.Sprintf("%s@%s", strings.TrimSpace(remoteIdentity), address), c.opts)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewSelfHealingServiceClient(conn).CommitSelfHealing(ctx, req)
}

func waitForTCPUnavailable(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err != nil {
			return nil
		}
		_ = conn.Close()
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("tcp endpoint still reachable: %s", address)
}

func tamperHexChar(hashHex string) string {
	trimmed := strings.TrimSpace(strings.ToLower(hashHex))
	if trimmed == "" {
		return "0"
	}
	if trimmed[0] == '0' {
		return "1" + trimmed[1:]
	}
	return "0" + trimmed[1:]
}
