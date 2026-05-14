//go:build system_test

package system

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// TestLEP6SupernodeRestartMidEpochPlannerConsistency exercises an
// operator-realistic restart of one real supernode while the real chain and the
// remaining supernodes continue running. It proves the restarted node's persisted
// SQLite state opens cleanly, the CASCADE data written before restart remains
// byte-identical on download, fresh CASCADE work still finalizes after restart,
// and runtime logs do not show replay/lock/panic symptoms.
func TestLEP6SupernodeRestartMidEpochPlannerConsistency(t *testing.T) {
	runLEP6SupernodeRestartMidEpochPlannerConsistency(t)
}

func TestLEP6CrashRestartMidHealResumeOrReclaim(t *testing.T) {
	runLEP6CrashRestartMidHealResumeOrReclaim(t)
}

func runLEP6SupernodeRestartMidEpochPlannerConsistency(t *testing.T) {
	t.Helper()
	os.Setenv("INTEGRATION_TEST", "true")
	os.Setenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER", "1")
	t.Cleanup(func() {
		os.Unsetenv("INTEGRATION_TEST")
		os.Unsetenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER")
	})

	const (
		epochLengthBlocks = uint64(12)
		lumeraGRPCAddr    = "localhost:9090"
		lumeraChainID     = "testing"
		fundAmount        = "10000000ulume"
		actionType        = "CASCADE"
	)

	t.Log("Phase 4A Step 1: configure genesis and start real chain")
	sut.ModifyGenesisJSON(t,
		SetStakingBondDenomUlume(t),
		SetActionParams(t),
		SetSupernodeMetricsParams(t),
		setSupernodeParamsForAuditTests(t),
		setAuditParamsForFastEpochs(t, epochLengthBlocks, 1, 1, 1, []uint32{4444}),
		setAuditMissingReportGraceForRuntimeE2E(t),
		setStorageTruthTestParams(t, "STORAGE_TRUTH_ENFORCEMENT_MODE_SHADOW", 1000, 500, 1000, 0, 10),
	)
	sut.StartChain(t)
	cli := NewLumeradCLI(t, sut, true)

	binaryPath := locateExecutable(sut.ExecBinary)
	homePath := filepath.Join(WorkDir, sut.outputDir)
	supernodeUsers := []struct {
		keyName  string
		mnemonic string
	}{
		{keyName: "testkey1", mnemonic: "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"},
		{keyName: "testkey2", mnemonic: "club party current length duck agent love into slide extend spawn sentence kangaroo chunk festival order plate rare public good include situate liar miss"},
		{keyName: "testkey3", mnemonic: "young envelope urban crucial denial zone toward mansion protect bonus exotic puppy resource pistol expand tell cupboard radio hurry world radio trust explain million"},
	}
	for _, user := range supernodeUsers {
		recoverChainKey(t, binaryPath, homePath, user.keyName, user.mnemonic)
	}

	t.Log("Phase 4A Step 2: register/fund real runtime supernodes and upload users")
	n0 := getRuntimeSupernodeIdentity(t, cli, "node0", "testkey1")
	n1 := getRuntimeSupernodeIdentity(t, cli, "node1", "testkey2")
	n2 := getRuntimeSupernodeIdentity(t, cli, "node2", "testkey3")
	registerRuntimeSupernode(t, cli, "node0", n0, "localhost:4444", "4445")
	registerRuntimeSupernode(t, cli, "node1", n1, "localhost:4446", "4447")
	registerRuntimeSupernode(t, cli, "node2", n2, "localhost:4448", "4449")
	for _, node := range []testNodeIdentity{n0, n1, n2} {
		cli.FundAddress(node.accAddr, fundAmount)
	}
	bootstrapRuntimeSupernodeEligibility(t, cli)
	sut.AwaitNextBlock(t)

	uploadUsers := generateLEP6UploadUsers(t, 2)
	for _, user := range uploadUsers {
		cli.FundAddress(user.userAddress, fundAmount)
	}
	sut.AwaitNextBlock(t)

	t.Cleanup(restoreLEP6SupernodeFixturesAfterMatrix(t))
	cmds := StartLEP6Supernodes(t)
	t.Cleanup(func() { StopAllSupernodes(cmds) })
	waitForLEP6SupernodePorts(t, 0, 30*time.Second)
	waitForLEP6SupernodePorts(t, 1, 30*time.Second)
	waitForLEP6SupernodePorts(t, 2, 30*time.Second)
	time.Sleep(40 * time.Second) // allow supernode P2P/DHT routing to settle before upload

	t.Log("Phase 4A Step 3: finalize a CASCADE action before restart")
	before := uploadSingleRestartCascade(t, cli, uploadUsers[0], lumeraGRPCAddr, lumeraChainID, actionType, "before-restart")

	t.Log("Phase 4A Step 4: kill and restart supernode 1 with the same basedir")
	killLEP6Supernode(t, cmds, 0)
	assertLEP6SQLiteIntegrity(t, filepath.Join(WorkDir, "supernode-lep6-data1"))
	cmds[0] = restartLEP6Supernode(t, 0)
	waitForLEP6SupernodePorts(t, 0, 45*time.Second)
	sut.AwaitNextBlock(t)
	time.Sleep(20 * time.Second) // let restarted node rejoin P2P routing tables

	t.Log("Phase 4A Step 5: prove pre-restart CASCADE remains byte-identical after restart")
	ctx := context.Background()
	_, beforeClient := newLEP6ActionClientsForKey(t, ctx, before.keyName, before.mnemonic, lumeraGRPCAddr, lumeraChainID)
	downloadAndAssertCascadeBytes(t, ctx, beforeClient, before.actionID, before.userAddress, t.TempDir(), before.payload, before.hash)

	t.Log("Phase 4A Step 6: finalize and download fresh CASCADE work after restart")
	after := uploadSingleRestartCascade(t, cli, uploadUsers[1], lumeraGRPCAddr, lumeraChainID, actionType, "after-restart")
	_, afterClient := newLEP6ActionClientsForKey(t, ctx, after.keyName, after.mnemonic, lumeraGRPCAddr, lumeraChainID)
	downloadAndAssertCascadeBytes(t, ctx, afterClient, after.actionID, after.userAddress, t.TempDir(), after.payload, after.hash)

	t.Log("Phase 4A Step 7: assert restarted runtime logs/state stayed clean")
	assertLEP6SQLiteIntegrity(t, filepath.Join(WorkDir, "supernode-lep6-data1"))
	assertLEP6SupernodeLogsDoNotContain(t, []string{"database is locked", "panic:", "duplicate proof report"})
}

func runLEP6CrashRestartMidHealResumeOrReclaim(t *testing.T) {
	t.Helper()
	os.Setenv("INTEGRATION_TEST", "true")
	os.Setenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER", "1")
	t.Cleanup(func() {
		os.Unsetenv("INTEGRATION_TEST")
		os.Unsetenv("LUMERA_SUPERNODE_DISABLE_HOST_REPORTER")
	})

	const (
		epochLengthBlocks = uint64(12)
		originHeight      = int64(1)
		lumeraGRPCAddr    = "localhost:9090"
		lumeraChainID     = "testing"
		fundAmount        = "10000000ulume"
		actionType        = "CASCADE"
	)

	t.Log("Phase 4B Step 1: configure genesis and start real chain")
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

	binaryPath := locateExecutable(sut.ExecBinary)
	homePath := filepath.Join(WorkDir, sut.outputDir)
	supernodeUsers := []struct {
		keyName  string
		mnemonic string
	}{
		{keyName: "testkey1", mnemonic: "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"},
		{keyName: "testkey2", mnemonic: "club party current length duck agent love into slide extend spawn sentence kangaroo chunk festival order plate rare public good include situate liar miss"},
		{keyName: "testkey3", mnemonic: "young envelope urban crucial denial zone toward mansion protect bonus exotic puppy resource pistol expand tell cupboard radio hurry world radio trust explain million"},
	}
	for _, user := range supernodeUsers {
		recoverChainKey(t, binaryPath, homePath, user.keyName, user.mnemonic)
	}

	t.Log("Phase 4B Step 2: register/fund real runtime supernodes and upload user")
	n0 := getRuntimeSupernodeIdentity(t, cli, "node0", "testkey1")
	n1 := getRuntimeSupernodeIdentity(t, cli, "node1", "testkey2")
	n2 := getRuntimeSupernodeIdentity(t, cli, "node2", "testkey3")
	nodes := []testNodeIdentity{n0, n1, n2}
	registerRuntimeSupernode(t, cli, "node0", n0, "localhost:4444", "4445")
	registerRuntimeSupernode(t, cli, "node1", n1, "localhost:4446", "4447")
	registerRuntimeSupernode(t, cli, "node2", n2, "localhost:4448", "4449")
	for _, node := range nodes {
		cli.FundAddress(node.accAddr, fundAmount)
	}
	bootstrapRuntimeSupernodeEligibility(t, cli)
	sut.AwaitNextBlock(t)

	uploadUsers := generateLEP6UploadUsers(t, 1)
	cli.FundAddress(uploadUsers[0].userAddress, fundAmount)
	sut.AwaitNextBlock(t)

	t.Cleanup(restoreLEP6SupernodeFixturesAfterMatrix(t))
	cmds := StartLEP6Supernodes(t)
	t.Cleanup(func() { StopAllSupernodes(cmds) })
	for idx := range nodes {
		waitForLEP6SupernodePorts(t, idx, 30*time.Second)
	}
	time.Sleep(40 * time.Second) // allow supernode P2P/DHT routing to settle before upload

	t.Log("Phase 4B Step 3: finalize CASCADE action and prove baseline download")
	upload := uploadSingleRestartCascade(t, cli, uploadUsers[0], lumeraGRPCAddr, lumeraChainID, actionType, "phase4b-heal-restart")
	ctx := context.Background()
	lumeraClient, actionClient := newLEP6ActionClientsForKey(t, ctx, upload.keyName, upload.mnemonic, lumeraGRPCAddr, lumeraChainID)
	t.Cleanup(func() { lumeraClient.Close() })
	artifactCounts := requireFinalizedCascadeArtifactCounts(t, ctx, lumeraClient, upload.actionID)
	downloadAndAssertCascadeBytes(t, ctx, actionClient, upload.actionID, upload.userAddress, t.TempDir(), upload.payload, upload.hash)

	t.Log("Phase 4B Step 4: submit deterministic storage-proof failures to schedule heal op")
	currentHeight := sut.AwaitNextBlock(t)
	epochID, epochStart := nextEpochAfterHeight(originHeight, epochLengthBlocks, currentHeight)
	epochEnd := epochStart + int64(epochLengthBlocks)
	awaitAtLeastHeight(t, epochStart)
	anchor := awaitCurrentEpochAnchorWithActiveSupernodes(t, epochID, n0.accAddr, n1.accAddr, n2.accAddr)
	require.ElementsMatch(t, []string{n0.accAddr, n1.accAddr, n2.accAddr}, anchor.ActiveSupernodeAccounts)

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
			upload.actionID,
			"phase4b-recent-hash-mismatch",
			"STORAGE_PROOF_BUCKET_TYPE_RECENT",
			"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
			artifactCounts.index,
		),
		"--storage-proof-results", buildStorageProofResultJSONWithClassAndCount(
			prober.accAddr,
			target.accAddr,
			upload.actionID,
			"phase4b-old-hash-mismatch",
			"STORAGE_PROOF_BUCKET_TYPE_OLD",
			"STORAGE_PROOF_RESULT_CLASS_HASH_MISMATCH",
			artifactCounts.index,
		),
	)
	reportResp := cli.CustomCommand(reportArgs...)
	RequireTxSuccess(t, reportResp)
	sut.AwaitNextBlock(t)

	ticketBefore, found := auditQueryTicketDeteriorationStateST(t, upload.actionID)
	require.True(t, found, "storage challenge failure must create deterioration state")
	require.GreaterOrEqual(t, ticketBefore.DeteriorationScore, int64(10), "ticket score must cross heal threshold before scheduling")

	t.Log("Phase 4B Step 5: wait for healer report, then crash/restart assigned healer before verification completes")
	awaitAtLeastHeight(t, epochEnd)
	sut.AwaitNextBlock(t)
	healOps := auditQueryHealOpsByTicketST(t, upload.actionID)
	require.Len(t, healOps, 1, "chain must schedule one heal op for the deteriorated CASCADE action ticket")
	scheduled := healOps[0]
	require.NotEmpty(t, scheduled.HealerSupernodeAccount)
	require.NotEmpty(t, scheduled.VerifierSupernodeAccounts)
	healerIdx := lep6SupernodeIndexForAccount(t, scheduled.HealerSupernodeAccount, nodes)
	verifierIdxs := make([]int, 0, len(scheduled.VerifierSupernodeAccounts))
	for _, verifier := range scheduled.VerifierSupernodeAccounts {
		verifierIdx := lep6SupernodeIndexForAccount(t, verifier, nodes)
		require.NotEqual(t, healerIdx, verifierIdx, "Phase 4B requires distinct healer and verifier so verification can be paused deterministically")
		verifierIdxs = append(verifierIdxs, verifierIdx)
	}
	for _, verifierIdx := range verifierIdxs {
		killLEP6Supernode(t, cmds, verifierIdx)
	}

	reported := awaitHealOpStatusByTicket(t, upload.actionID, scheduled.HealOpId, audittypes.HealOpStatus_HEAL_OP_STATUS_HEALER_REPORTED, 4*time.Minute)
	require.NotEmpty(t, reported.ResultHash, "healer must publish reconstructed manifest hash before crash")
	healerDataDir := dataDirForSupernodeAccount(t, reported.HealerSupernodeAccount, nodes...)
	stagingDir := locateLEP6HealStagingDir(t, healerDataDir, reported.HealOpId)
	require.DirExists(t, stagingDir, "healer-reported op must have persisted staging before crash")

	killLEP6Supernode(t, cmds, healerIdx)
	assertLEP6SQLiteIntegrity(t, filepath.Join(WorkDir, fmt.Sprintf("supernode-lep6-data%d", healerIdx+1)))
	cmds[healerIdx] = restartLEP6Supernode(t, healerIdx)
	waitForLEP6SupernodePorts(t, healerIdx, 45*time.Second)
	for _, verifierIdx := range verifierIdxs {
		cmds[verifierIdx] = restartLEP6Supernode(t, verifierIdx)
		waitForLEP6SupernodePorts(t, verifierIdx, 45*time.Second)
	}
	sut.AwaitNextBlock(t)

	t.Log("Phase 4B Step 6: assert restarted healer keeps persisted claim state available without requiring DHT publish")
	time.Sleep(20 * time.Second)
	currentOps := auditQueryHealOpsByTicketST(t, upload.actionID)
	var current audittypes.HealOp
	for _, op := range currentOps {
		if op.HealOpId == reported.HealOpId {
			current = op
			break
		}
	}
	require.Equal(t, reported.HealOpId, current.HealOpId, "restarted healer op must remain queryable")
	require.Equal(t, reported.ResultHash, current.ResultHash, "restart path must preserve healer manifest hash")
	require.NotContains(t, []audittypes.HealOpStatus{
		audittypes.HealOpStatus_HEAL_OP_STATUS_FAILED,
		audittypes.HealOpStatus_HEAL_OP_STATUS_EXPIRED,
	}, current.Status, "restart path must not drive the heal op to a terminal failure")
	require.DirExists(t, stagingDir, "restart path must preserve staging for retry; DHT publish/cleanup is covered outside Phase 4B")
	assertLEP6SupernodeLogsDoNotContain(t, []string{"database is locked", "panic:", "duplicate proof report", "orphaned heal"})
}

func locateLEP6HealStagingDir(t *testing.T, dataDir string, healOpID uint64) string {
	t.Helper()
	candidates := []string{
		filepath.Join(dataDir, "heal-staging", fmt.Sprintf("%d", healOpID)),
		filepath.Join(dataDir, filepath.Base(dataDir), "heal-staging", fmt.Sprintf("%d", healOpID)),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	t.Fatalf("no heal staging dir for op %d under %s; checked %v", healOpID, dataDir, candidates)
	return ""
}

func lep6SupernodeIndexForAccount(t *testing.T, account string, nodes []testNodeIdentity) int {
	t.Helper()
	for idx, node := range nodes {
		if node.accAddr == account {
			return idx
		}
	}
	t.Fatalf("no LEP-6 supernode fixture index for account %s", account)
	return -1
}

func uploadSingleRestartCascade(t *testing.T, cli *LumeradCli, user lep6UploadUser, lumeraGRPCAddr, lumeraChainID, actionType, label string) concurrentCascadeUpload {
	t.Helper()
	ctx := context.Background()
	lumeraClient, actionClient := newLEP6ActionClientsForKey(t, ctx, user.keyName, user.mnemonic, lumeraGRPCAddr, lumeraChainID)
	t.Cleanup(func() { lumeraClient.Close() })

	payload := []byte(fmt.Sprintf("lep6 phase4a restart cascade payload %s\n%s\n", label, strings.Repeat(label+"-chunk-", 64)))
	path := filepath.Join(t.TempDir(), fmt.Sprintf("phase4a-%s.txt", label))
	require.NoError(t, os.WriteFile(path, payload, 0o600))

	actionID := requestAndStartCascadeAction(t, ctx, cli, lumeraClient, actionClient, path, actionType)
	require.NoError(t, waitForActionStateWithClient(ctx, lumeraClient, actionID, actiontypes.ActionStateDone))
	requireFinalizedCascadeArtifactCounts(t, ctx, lumeraClient, actionID)
	return concurrentCascadeUpload{
		actionID:    actionID,
		keyName:     user.keyName,
		mnemonic:    user.mnemonic,
		userAddress: user.userAddress,
		payload:     payload,
		hash:        sha256.Sum256(payload),
	}
}

func killLEP6Supernode(t *testing.T, cmds []*exec.Cmd, idx int) {
	t.Helper()
	require.Greater(t, len(cmds), idx)
	require.NotNil(t, cmds[idx])
	require.NotNil(t, cmds[idx].Process)
	pid := cmds[idx].Process.Pid
	t.Logf("killing LEP-6 supernode %d pid=%d", idx+1, pid)
	require.NoError(t, cmds[idx].Process.Kill())
	_, err := cmds[idx].Process.Wait()
	if err != nil && !strings.Contains(err.Error(), "waitid: no child processes") {
		t.Logf("wait after killing supernode %d returned: %v", idx+1, err)
	}
}

func restartLEP6Supernode(t *testing.T, idx int) *exec.Cmd {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	dataDir := filepath.Join(wd, fmt.Sprintf("supernode-lep6-data%d", idx+1))
	binPath := filepath.Join(dataDir, "supernode")
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("supernode binary not found at %s; did you run setup?", binPath)
	}
	logPath := filepath.Join(wd, fmt.Sprintf("supernode-lep6%d.out", idx))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })

	cmd := exec.Command(binPath, "start", "--basedir", dataDir)
	cmd.Stdout = io.MultiWriter(os.Stdout, logFile)
	cmd.Stderr = io.MultiWriter(os.Stderr, logFile)
	t.Logf("restarting supernode %d from directory: %s", idx+1, dataDir)
	require.NoError(t, cmd.Start())
	return cmd
}

func waitForLEP6SupernodePorts(t *testing.T, idx int, timeout time.Duration) {
	t.Helper()
	ports := []int{4444 + idx*2, 4445 + idx*2}
	for _, port := range ports {
		port := port
		require.Eventually(t, func() bool {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err != nil {
				return false
			}
			_ = conn.Close()
			return true
		}, timeout, time.Second, "supernode %d port %d should become reachable", idx+1, port)
	}
}

func assertLEP6SQLiteIntegrity(t *testing.T, baseDir string) {
	t.Helper()
	var sqliteFiles []string
	require.NoError(t, filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".sqlite3") || strings.HasSuffix(name, ".db") {
			sqliteFiles = append(sqliteFiles, path)
		}
		return nil
	}))
	require.NotEmpty(t, sqliteFiles, "expected at least one SQLite file under %s", baseDir)
	for _, path := range sqliteFiles {
		db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", path))
		require.NoError(t, err, "open sqlite %s", path)
		var result string
		err = db.QueryRow("PRAGMA integrity_check").Scan(&result)
		closeErr := db.Close()
		require.NoError(t, err, "integrity_check %s", path)
		require.NoError(t, closeErr, "close sqlite %s", path)
		require.Equal(t, "ok", strings.ToLower(result), "sqlite integrity_check %s", path)
	}
}
