//go:build system_test

package system

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/sdk/event"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// TestLEP6RuntimeE2E_CascadeChallengeHealVerifyAndStore mirrors the shape of
// TestCascadeE2E, but extends it through the LEP-6 runtime path:
//
//  1. start real lumerad + three real supernode processes;
//  2. upload a real CASCADE action and prove normal download works;
//  3. submit a real storage-challenge epoch report for that action/ticket;
//  4. wait for chain to schedule a heal-op;
//  5. let the assigned healer supernode reconstruct+stage and claim;
//  6. let assigned verifier supernodes fetch healer-served bytes and verify;
//  7. wait for chain VERIFIED and finalizer publish;
//  8. download the action again and assert bytes still match the original.
//
// The storage-challenge report is driven by the test so the failure is
// deterministic; the healer/verifier/finalizer data-plane is driven by the real
// supernode self_healing services.
func TestLEP6RuntimeE2E_CascadeChallengeHealVerifyAndStore(t *testing.T) {
	os.Setenv("INTEGRATION_TEST", "true")
	os.Setenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER", "1")
	defer os.Unsetenv("INTEGRATION_TEST")
	defer os.Unsetenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER")

	const (
		epochLengthBlocks = uint64(12)
		originHeight      = int64(1)
		lumeraGRPCAddr    = "localhost:9090"
		lumeraChainID     = "testing"
		testKeyName       = "testkey1"
		testMnemonic      = "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"
		testKey2Mnemonic  = "club party current length duck agent love into slide extend spawn sentence kangaroo chunk festival order plate rare public good include situate liar miss"
		testKey3Mnemonic  = "young envelope urban crucial denial zone toward mansion protect bonus exotic puppy resource pistol expand tell cupboard radio hurry world radio trust explain million"
		expectedAddress   = "lumera1em87kgrvgttrkvuamtetyaagjrhnu3vjy44at4"
		userKeyName       = "user"
		userMnemonic      = "little tone alley oval festival gloom sting asthma crime select swap auto when trip luxury pact risk sister pencil about crisp upon opera timber"
		fundAmount        = "1000000ulume"
		actionType        = "CASCADE"
	)

	t.Log("Step 1: configure genesis and start chain")
	sut.ModifyGenesisJSON(t,
		SetStakingBondDenomUlume(t),
		SetActionParams(t),
		SetSupernodeMetricsParams(t),
		setSupernodeParamsForAuditTests(t),
		setAuditParamsForFastEpochs(t, epochLengthBlocks, 1, 1, 1, []uint32{4444}),
		setAuditMissingReportGraceForRuntimeE2E(t),
		setStorageTruthTestParams(t, "STORAGE_TRUTH_ENFORCEMENT_MODE_FULL", 1000, 500, 10, 0, 10),
	)
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)

	t.Log("Step 2: register and fund three supernodes")
	binaryPath := locateExecutable(sut.ExecBinary)
	homePath := filepath.Join(WorkDir, sut.outputDir)
	recoverChainKey(t, binaryPath, homePath, testKeyName, testMnemonic)
	recoverChainKey(t, binaryPath, homePath, "testkey2", testKey2Mnemonic)
	recoverChainKey(t, binaryPath, homePath, "testkey3", testKey3Mnemonic)
	recoverChainKey(t, binaryPath, homePath, userKeyName, userMnemonic)

	n0 := getRuntimeSupernodeIdentity(t, cli, "node0", "testkey1")
	n1 := getRuntimeSupernodeIdentity(t, cli, "node1", "testkey2")
	n2 := getRuntimeSupernodeIdentity(t, cli, "node2", "testkey3")
	registerRuntimeSupernode(t, cli, "node0", n0, "localhost:4444", "4445")
	registerRuntimeSupernode(t, cli, "node1", n1, "localhost:4446", "4447")
	registerRuntimeSupernode(t, cli, "node2", n2, "localhost:4448", "4449")
	cli.FundAddress(n0.accAddr, "100000ulume")
	cli.FundAddress(n1.accAddr, "100000ulume")
	cli.FundAddress(n2.accAddr, "100000ulume")
	bootstrapRuntimeSupernodeEligibility(t, cli)

	t.Log("Step 3: recover user/test keys and start real supernodes")
	recoveredAddress := cli.GetKeyAddr(testKeyName)
	require.Equal(t, expectedAddress, recoveredAddress)
	userAddress := cli.GetKeyAddr(userKeyName)
	cli.FundAddress(recoveredAddress, fundAmount)
	cli.FundAddress(userAddress, fundAmount)
	sut.AwaitNextBlock(t)

	cmds := StartLEP6Supernodes(t)
	defer StopAllSupernodes(cmds)
	time.Sleep(40 * time.Second) // Match Cascade e2e: allow supernode P2P/DHT routing to settle before upload.

	t.Log("Step 4: upload a real Cascade action through the SDK/supernodes")
	ctx := context.Background()
	kr, err := keyring.InitKeyring(config.KeyringConfig{Backend: "memory", Dir: ""})
	require.NoError(t, err)
	_, err = keyring.RecoverAccountFromMnemonic(kr, testKeyName, testMnemonic)
	require.NoError(t, err)
	userRecord, err := keyring.RecoverAccountFromMnemonic(kr, userKeyName, userMnemonic)
	require.NoError(t, err)
	userLocalAddr, err := userRecord.GetAddress()
	require.NoError(t, err)
	require.Equal(t, userAddress, userLocalAddr.String())

	lumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, userKeyName, kr)
	require.NoError(t, err)
	lumeraClient, err := lumera.NewClient(ctx, lumeraCfg)
	require.NoError(t, err)
	defer lumeraClient.Close()

	actionClient, err := action.NewClient(ctx, sdkconfig.Config{
		Account: sdkconfig.AccountConfig{KeyName: userKeyName, Keyring: kr},
		Lumera:  sdkconfig.LumeraConfig{GRPCAddr: lumeraGRPCAddr, ChainID: lumeraChainID},
	}, nil)
	require.NoError(t, err)

	testFileFullpath := filepath.Join("test.txt")
	originalData := readFileBytes(t, testFileFullpath)
	originalHash := sha256.Sum256(originalData)

	actionID := requestAndStartCascadeAction(t, ctx, cli, lumeraClient, actionClient, testFileFullpath, actionType)
	require.NoError(t, waitForActionStateWithClient(ctx, lumeraClient, actionID, actiontypes.ActionStateDone))
	artifactCounts := requireFinalizedCascadeArtifactCounts(t, ctx, lumeraClient, actionID)

	t.Log("Step 5: prove pre-heal Cascade download works")
	preHealDir := t.TempDir()
	downloadAndAssertCascadeBytes(t, ctx, actionClient, actionID, userAddress, preHealDir, originalData, originalHash)

	t.Log("Step 6: submit deterministic storage-challenge report for the Cascade action ticket")
	currentHeight := sut.AwaitNextBlock(t)
	epochID, epochStart := nextEpochAfterHeight(originHeight, epochLengthBlocks, currentHeight)
	epochEnd := epochStart + int64(epochLengthBlocks)
	awaitAtLeastHeight(t, epochStart)
	anchor := awaitCurrentEpochAnchorWithActiveSupernodes(t, epochID, n0.accAddr, n1.accAddr, n2.accAddr)
	require.ElementsMatch(t, []string{n0.accAddr, n1.accAddr, n2.accAddr}, anchor.ActiveSupernodeAccounts)

	nodes := []testNodeIdentity{n0, n1, n2}
	proberResp, prober, target := findAssignedProberAndTarget(t, epochID, nodes)
	portStates := openPortStates(proberResp.RequiredOpenPorts)
	reportArgs := []string{
		"tx", "audit", "submit-epoch-report",
		strconv.FormatUint(epochID, 10),
		auditHostReportJSON(portStates),
		"--from", prober.nodeName,
		"--gas", "500000",
	}
	for _, assignedTarget := range proberResp.TargetSupernodeAccounts {
		reportArgs = append(reportArgs, "--storage-challenge-observations", storageChallengeObservationJSON(assignedTarget, portStates))
	}
	reportArgs = append(reportArgs,
		"--storage-proof-results", buildStorageProofResultJSONWithClassAndCount(
			prober.accAddr,
			target.accAddr,
			actionID,
			"runtime-e2e-recent-hash-mismatch",
			"STORAGE_PROOF_BUCKET_TYPE_RECENT",
			"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
			artifactCounts.index,
		),
		"--storage-proof-results", buildStorageProofResultJSONWithClassAndCount(
			prober.accAddr,
			target.accAddr,
			actionID,
			"runtime-e2e-old-hash-mismatch",
			"STORAGE_PROOF_BUCKET_TYPE_OLD",
			"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
			artifactCounts.index,
		),
	)
	reportResp := cli.CustomCommand(reportArgs...)
	RequireTxSuccess(t, reportResp)
	sut.AwaitNextBlock(t)

	ticketBefore, found := auditQueryTicketDeteriorationStateST(t, actionID)
	require.True(t, found, "storage challenge failure for the action/ticket must create deterioration state")
	require.GreaterOrEqual(t, ticketBefore.DeteriorationScore, int64(10), "ticket score must cross heal threshold before scheduling")

	t.Log("Step 7: wait for chain heal-op schedule and real supernode self-healing runtime")
	awaitAtLeastHeight(t, epochEnd)
	sut.AwaitNextBlock(t)
	healOps := auditQueryHealOpsByTicketST(t, actionID)
	require.Len(t, healOps, 1, "chain must schedule one heal op for the deteriorated Cascade action ticket")
	healOp := healOps[0]
	require.False(t, isFinalStatusForRuntimeE2E(healOp.Status), "newly observed heal op must not already be final: %s", healOp.Status.String())
	require.NotEmpty(t, healOp.HealerSupernodeAccount)
	require.NotEmpty(t, healOp.VerifierSupernodeAccounts)

	verified := awaitAnyHealOpStatusByTicket(t, actionID, audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED, 6*time.Minute)
	require.NotEmpty(t, verified.ResultHash, "real healer must submit the reconstructed file BLAKE3 manifest hash before verifier quorum")

	healerDataDir := dataDirForSupernodeAccount(t, verified.HealerSupernodeAccount, n0, n1, n2)
	stagingDir := filepath.Join(healerDataDir, "heal-staging", fmt.Sprintf("%d", verified.HealOpId))
	awaitStagingDirRemoved(t, stagingDir, 90*time.Second)

	ticketAfter, found := auditQueryTicketDeteriorationStateST(t, actionID)
	require.True(t, found)
	require.Less(t, ticketAfter.DeteriorationScore, ticketBefore.DeteriorationScore, "VERIFIED heal must reduce ticket deterioration")

	t.Log("Step 8: prove post-heal Cascade data remains retrievable and byte-identical")
	postHealDir := t.TempDir()
	downloadAndAssertCascadeBytes(t, ctx, actionClient, actionID, userAddress, postHealDir, originalData, originalHash)
}

type finalizedCascadeArtifactCounts struct {
	index  uint32
	symbol uint32
}

func requireFinalizedCascadeArtifactCounts(t *testing.T, ctx context.Context, client lumera.Client, actionID string) finalizedCascadeArtifactCounts {
	t.Helper()
	resp, err := client.Action().GetAction(ctx, actionID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Action)
	meta, err := cascadekit.UnmarshalCascadeMetadata(resp.Action.Metadata)
	require.NoError(t, err)
	require.NotZero(t, meta.IndexArtifactCount, "finalized Cascade action metadata must include LEP-6 index artifact count")
	require.NotZero(t, meta.SymbolArtifactCount, "finalized Cascade action metadata must include LEP-6 symbol artifact count")
	t.Logf("Finalized Cascade artifact counts for action %s: index=%d symbol=%d", actionID, meta.IndexArtifactCount, meta.SymbolArtifactCount)
	return finalizedCascadeArtifactCounts{index: meta.IndexArtifactCount, symbol: meta.SymbolArtifactCount}
}

func recoverChainKey(t *testing.T, binaryPath, homePath, keyName, mnemonic string) {
	t.Helper()
	cmd := exec.Command(binaryPath, "keys", "add", keyName, "--recover", "--keyring-backend=test", "--home", homePath)
	cmd.Stdin = strings.NewReader(mnemonic + "\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "recover key %s failed: %s", keyName, string(out))
}

func setAuditMissingReportGraceForRuntimeE2E(t *testing.T) GenesisMutator {
	return func(genesis []byte) []byte {
		t.Helper()
		state, err := sjson.SetRawBytes(genesis, "app_state.audit.params.consecutive_epochs_to_postpone", []byte("100"))
		require.NoError(t, err)
		return state
	}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	return b
}

func getRuntimeSupernodeIdentity(t *testing.T, cli *LumeradCli, validatorKey, supernodeKey string) testNodeIdentity {
	t.Helper()
	accAddr := cli.GetKeyAddr(supernodeKey)
	valAddr := strings.TrimSpace(cli.Keys("keys", "show", validatorKey, "--bech", "val", "-a"))
	require.NotEmpty(t, accAddr)
	require.NotEmpty(t, valAddr)
	return testNodeIdentity{nodeName: supernodeKey, accAddr: accAddr, valAddr: valAddr}
}

func registerRuntimeSupernode(t *testing.T, cli *LumeradCli, signerKey string, id testNodeIdentity, grpcAddress, p2pPort string) {
	t.Helper()
	resp := cli.CustomCommand(
		"tx", "supernode", "register-supernode",
		id.valAddr,
		grpcAddress,
		id.accAddr,
		"--p2p-port", p2pPort,
		"--from", signerKey,
	)
	RequireTxSuccess(t, resp)
	sut.AwaitNextBlock(t)
}

func bootstrapRuntimeSupernodeEligibility(t *testing.T, cli *LumeradCli) {
	t.Helper()
	listResp := cli.CustomQuery("query", "supernode", "list-supernodes", "--output", "json")
	t.Logf("Registered supernodes response: %s", listResp)
	require.NotEqual(t, "{}", strings.TrimSpace(listResp), "registered supernodes must be visible before Cascade bootstrap")

	queryHeight := sut.AwaitNextBlock(t)
	resp := cli.CustomQuery(
		"query", "supernode", "get-top-supernodes-for-block",
		fmt.Sprint(queryHeight),
		"--output", "json",
	)
	t.Logf("Bootstrap top-supernodes response at height %d: %s", queryHeight, resp)
	require.NotEmpty(t, strings.TrimSpace(resp), "top-supernodes bootstrap query must return a response")
}

func requestAndStartCascadeAction(t *testing.T, ctx context.Context, cli *LumeradCli, lc lumera.Client, ac action.Client, filePath, actionType string) string {
	t.Helper()
	meta, price, expiration, err := ac.BuildCascadeMetadataFromFile(ctx, filePath, false, "")
	require.NoError(t, err)
	metaBytes, err := json.Marshal(meta)
	require.NoError(t, err)
	fi, err := os.Stat(filePath)
	require.NoError(t, err)
	fileSizeKbs := (fi.Size() + 1023) / 1024
	resp, err := lc.ActionMsg().RequestAction(ctx, actionType, string(metaBytes), price, expiration, strconv.FormatInt(fileSizeKbs, 10))
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Zero(t, resp.TxResponse.Code, "RequestAction tx failed: %s", resp.TxResponse.RawLog)
	sut.AwaitNextBlock(t)

	txResp := awaitTxQuery(t, cli, resp.TxResponse.TxHash, 45*time.Second)
	require.Equal(t, int64(0), gjson.Get(txResp, "code").Int(), "RequestAction tx query failed: %s", txResp)
	actionID := extractActionIDFromTx(t, txResp)

	txHashCh := make(chan string, 1)
	completionCh := make(chan struct{}, 1)
	errCh := make(chan string, 1)
	err = ac.SubscribeToAllEvents(context.Background(), func(ctx context.Context, e event.Event) {
		switch e.Type {
		case event.SDKTaskTxHashReceived:
			if txHash, ok := e.Data[event.KeyTxHash].(string); ok && txHash != "" {
				select {
				case txHashCh <- txHash:
				default:
				}
			}
		case event.SDKTaskCompleted:
			select {
			case completionCh <- struct{}{}:
			default:
			}
		case event.SDKTaskFailed:
			msg, _ := e.Data[event.KeyError].(string)
			if msg == "" {
				msg = "cascade task failed without an SDK error message"
			}
			select {
			case errCh <- msg:
			default:
			}
		}
	})
	require.NoError(t, err)

	time.Sleep(5 * time.Second)
	sig, err := ac.GenerateStartCascadeSignatureFromFile(ctx, filePath)
	require.NoError(t, err)
	_, err = ac.StartCascade(ctx, filePath, actionID, sig)
	require.NoError(t, err)

	var finalizeTxHash string
	completed := false
	timeout := time.After(3 * time.Minute)
	for finalizeTxHash == "" || !completed {
		select {
		case h := <-txHashCh:
			if finalizeTxHash == "" {
				finalizeTxHash = h
			}
		case <-completionCh:
			completed = true
		case msg := <-errCh:
			t.Fatalf("cascade task reported failure: %s", msg)
		case <-timeout:
			t.Fatalf("timeout waiting for cascade SDK events; finalizeTxHash=%q completed=%v", finalizeTxHash, completed)
		}
	}
	finalizeResp := awaitTxQuery(t, cli, finalizeTxHash, 45*time.Second)
	require.Equal(t, int64(0), gjson.Get(finalizeResp, "code").Int(), "Cascade finalize tx failed: %s", finalizeResp)
	return actionID
}

func awaitTxQuery(t *testing.T, cli *LumeradCli, txHash string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	binaryPath := locateExecutable(sut.ExecBinary)
	for time.Now().Before(deadline) {
		cmd := exec.Command(binaryPath, "query", "tx", txHash, "--output", "json", "--node", "tcp://localhost:26657")
		outBytes, _ := cmd.CombinedOutput()
		out := string(outBytes)
		last = out
		lower := strings.ToLower(out)
		if strings.Contains(lower, "tx not found") || strings.Contains(lower, "rpc error") || strings.Contains(lower, "usage:") {
			time.Sleep(time.Second)
			continue
		}
		return out
	}
	t.Fatalf("tx %s was not queryable before timeout; last=%s", txHash, last)
	return ""
}

func extractActionIDFromTx(t *testing.T, txResp string) string {
	t.Helper()
	for _, event := range gjson.Get(txResp, "events").Array() {
		if event.Get("type").String() != "action_registered" {
			continue
		}
		for _, attr := range event.Get("attributes").Array() {
			if attr.Get("key").String() == "action_id" {
				return attr.Get("value").String()
			}
		}
	}
	t.Fatalf("action_id not found in tx response: %s", txResp)
	return ""
}

func downloadAndAssertCascadeBytes(t *testing.T, ctx context.Context, ac action.Client, actionID, userAddress, outputBaseDir string, originalData []byte, originalHash [32]byte) {
	t.Helper()
	sig, err := ac.GenerateDownloadSignature(ctx, actionID, userAddress)
	require.NoError(t, err)
	_, err = ac.DownloadCascade(ctx, actionID, outputBaseDir, sig)
	require.NoError(t, err)
	outDir := filepath.Join(outputBaseDir, actionID)
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(outDir)
		return err == nil && len(entries) > 0
	}, 45*time.Second, time.Second, "download output directory should contain reconstructed file")
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	var downloadedPath string
	for _, entry := range entries {
		if !entry.IsDir() {
			downloadedPath = filepath.Join(outDir, entry.Name())
			break
		}
	}
	require.NotEmpty(t, downloadedPath, "download output must contain a file")
	downloaded := readFileBytes(t, downloadedPath)
	require.Equal(t, len(originalData), len(downloaded), "downloaded size must match original")
	require.Equal(t, originalHash, sha256.Sum256(downloaded), "downloaded hash must match original")
}

func isFinalStatusForRuntimeE2E(status audittypes.HealOpStatus) bool {
	switch status {
	case audittypes.HealOpStatus_HEAL_OP_STATUS_VERIFIED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED:
		return true
	default:
		return false
	}
}

func awaitAnyHealOpStatusByTicket(t *testing.T, ticketID string, status audittypes.HealOpStatus, timeout time.Duration) audittypes.HealOp {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []audittypes.HealOp
	for time.Now().Before(deadline) {
		healOps := auditQueryHealOpsByTicketST(t, ticketID)
		last = healOps
		for _, op := range healOps {
			if op.Status == status {
				return op
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no heal op for ticket %s reached %s before timeout; last=%+v", ticketID, status.String(), last)
	return audittypes.HealOp{}
}

func awaitHealOpStatusByTicket(t *testing.T, ticketID string, healOpID uint64, status audittypes.HealOpStatus, timeout time.Duration) audittypes.HealOp {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last audittypes.HealOp
	for time.Now().Before(deadline) {
		for _, op := range auditQueryHealOpsByTicketST(t, ticketID) {
			if op.HealOpId == healOpID {
				last = op
				if op.Status == status {
					return op
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("heal op %d for ticket %s did not reach %s before timeout; last=%+v", healOpID, ticketID, status.String(), last)
	return audittypes.HealOp{}
}

func dataDirForSupernodeAccount(t *testing.T, account string, nodes ...testNodeIdentity) string {
	t.Helper()
	for i, node := range nodes {
		if node.accAddr == account {
			return filepath.Join(".", fmt.Sprintf("supernode-lep6-data%d", i+1))
		}
	}
	t.Fatalf("supernode account %q not found in test nodes", account)
	return ""
}

func awaitStagingDirRemoved(t *testing.T, stagingDir string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(stagingDir)
		return os.IsNotExist(err)
	}, timeout, 3*time.Second, "verified heal finalizer should publish then remove staging dir %s", stagingDir)
}
