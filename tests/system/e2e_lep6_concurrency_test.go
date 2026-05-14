//go:build system_test

package system

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sdkhd "github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"

	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/stretchr/testify/require"
)

type concurrentCascadeUpload struct {
	actionID    string
	keyName     string
	mnemonic    string
	userAddress string
	payload     []byte
	hash        [32]byte
}

type lep6UploadUser struct {
	keyName     string
	mnemonic    string
	userAddress string
}

// TestLEP6ConcurrentCascadesContendedReporter exercises multiple simultaneous
// CASCADE uploads against the real LEP-6 system-test fixture. The test targets
// runtime contention risks observed around SQLite/P2P/Cascade orchestration: all
// actions must finalize, remain byte-identical on download, and the supernode
// logs must not contain lock/panic/duplicate-report failures.
func TestLEP6ConcurrentCascadesContendedReporter(t *testing.T) {
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
		// Keep runtime supernodes above SDK's 1 LUME eligibility threshold after
		// paying finalize tx fees for concurrent CASCADE actions; otherwise the
		// post-upload download prefilter can find zero eligible targets.
		fundAmount = "10000000ulume"
		actionType = "CASCADE"
	)

	t.Log("Phase 3 Step 1: configure genesis and start real chain")
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

	t.Log("Phase 3 Step 2: register and fund three real runtime supernodes/users")
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
	uploadUsers := generateLEP6UploadUsers(t, 3)
	for _, user := range uploadUsers {
		cli.FundAddress(user.userAddress, fundAmount)
	}
	sut.AwaitNextBlock(t)

	t.Cleanup(restoreLEP6SupernodeFixturesAfterMatrix(t))
	cmds := StartLEP6Supernodes(t)
	t.Cleanup(func() { StopAllSupernodes(cmds) })
	time.Sleep(40 * time.Second) // allow supernode P2P/DHT routing to settle before concurrent upload

	count := lep6ConcurrentActionCount(t, len(uploadUsers))
	t.Logf("Phase 3 Step 3: upload %d CASCADE actions concurrently", count)
	uploads := uploadConcurrentCascadesForLEP6(t, cli, uploadUsers[:count], lumeraGRPCAddr, lumeraChainID, actionType)
	require.Len(t, uploads, count)

	t.Log("Phase 3 Step 4: verify all concurrent actions download byte-identical")
	for _, upload := range uploads {
		ctx := context.Background()
		_, actionClient := newLEP6ActionClientsForKey(t, ctx, upload.keyName, upload.mnemonic, lumeraGRPCAddr, lumeraChainID)
		downloadAndAssertCascadeBytes(t, ctx, actionClient, upload.actionID, upload.userAddress, t.TempDir(), upload.payload, upload.hash)
	}

	t.Log("Phase 3 Step 5: assert runtime logs stayed clean under concurrent CASCADE load")
	assertLEP6SupernodeLogsDoNotContain(t, []string{"database is locked", "panic:", "duplicate proof report"})
}

func lep6ConcurrentActionCount(t *testing.T, max int) int {
	t.Helper()
	count := 3
	if raw := strings.TrimSpace(os.Getenv("LEP6_CONCURRENT_ACTIONS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "LEP6_CONCURRENT_ACTIONS must be an integer")
		count = parsed
	}
	require.GreaterOrEqual(t, count, 1)
	require.LessOrEqual(t, count, max, "this PR-blocking test has %d deterministic upload accounts available", max)
	return count
}

func uploadConcurrentCascadesForLEP6(
	t *testing.T,
	cli *LumeradCli,
	users []lep6UploadUser,
	lumeraGRPCAddr string,
	lumeraChainID string,
	actionType string,
) []concurrentCascadeUpload {
	t.Helper()

	uploads := make([]concurrentCascadeUpload, 0, len(users))
	var mu sync.Mutex
	t.Run("concurrent-uploads", func(t *testing.T) {
		for i, user := range users {
			i, user := i, user
			t.Run(fmt.Sprintf("upload-%d-%s", i+1, user.keyName), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				lumeraClient, actionClient := newLEP6ActionClientsForKey(t, ctx, user.keyName, user.mnemonic, lumeraGRPCAddr, lumeraChainID)
				t.Cleanup(func() { lumeraClient.Close() })

				payload := []byte(fmt.Sprintf("lep6 phase3 concurrent cascade payload %d\n%s\n", i+1, strings.Repeat(fmt.Sprintf("chunk-%02d-", i+1), 64)))
				path := filepath.Join(t.TempDir(), fmt.Sprintf("phase3-concurrent-%d.txt", i+1))
				require.NoError(t, os.WriteFile(path, payload, 0o600))

				actionID := requestAndStartCascadeAction(t, ctx, cli, lumeraClient, actionClient, path, actionType)
				require.NoError(t, waitForActionStateWithClient(ctx, lumeraClient, actionID, actiontypes.ActionStateDone))
				requireFinalizedCascadeArtifactCounts(t, ctx, lumeraClient, actionID)

				mu.Lock()
				uploads = append(uploads, concurrentCascadeUpload{
					actionID:    actionID,
					keyName:     user.keyName,
					mnemonic:    user.mnemonic,
					userAddress: user.userAddress,
					payload:     payload,
					hash:        sha256.Sum256(payload),
				})
				mu.Unlock()
			})
		}
	})
	return uploads
}

func generateLEP6UploadUsers(t *testing.T, count int) []lep6UploadUser {
	t.Helper()
	kr, err := keyring.InitKeyring(config.KeyringConfig{Backend: "memory", Dir: ""})
	require.NoError(t, err)

	users := make([]lep6UploadUser, 0, count)
	for i := 0; i < count; i++ {
		keyName := fmt.Sprintf("phase3-upload-user-%d", i+1)
		record, mnemonic, err := kr.NewMnemonic(keyName, sdkkeyring.English, keyring.DefaultHDPath, keyring.DefaultBIP39Passphrase, sdkhd.Secp256k1)
		require.NoError(t, err)
		addr, err := record.GetAddress()
		require.NoError(t, err)
		users = append(users, lep6UploadUser{keyName: keyName, mnemonic: mnemonic, userAddress: addr.String()})
	}
	return users
}

func newLEP6ActionClientsForKey(t *testing.T, ctx context.Context, keyName, mnemonic, lumeraGRPCAddr, lumeraChainID string) (lumera.Client, action.Client) {
	t.Helper()
	kr, err := keyring.InitKeyring(config.KeyringConfig{Backend: "memory", Dir: ""})
	require.NoError(t, err)
	_, err = keyring.RecoverAccountFromMnemonic(kr, keyName, mnemonic)
	require.NoError(t, err)

	lumeraCfg, err := lumera.NewConfig(lumeraGRPCAddr, lumeraChainID, keyName, kr)
	require.NoError(t, err)
	lumeraClient, err := lumera.NewClient(ctx, lumeraCfg)
	require.NoError(t, err)
	actionClient, err := action.NewClient(ctx, sdkconfig.Config{
		Account: sdkconfig.AccountConfig{KeyName: keyName, Keyring: kr},
		Lumera:  sdkconfig.LumeraConfig{GRPCAddr: lumeraGRPCAddr, ChainID: lumeraChainID},
	}, nil)
	require.NoError(t, err)
	return lumeraClient, actionClient
}

func assertLEP6SupernodeLogsDoNotContain(t *testing.T, patterns []string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		path := filepath.Join(WorkDir, fmt.Sprintf("supernode-lep6%d.out", i))
		data, err := os.ReadFile(path)
		require.NoError(t, err, "read supernode log %s", path)
		lower := strings.ToLower(string(data))
		for _, pattern := range patterns {
			require.NotContains(t, lower, strings.ToLower(pattern), "supernode log %s contains forbidden runtime pattern %q", path, pattern)
		}
	}
}
