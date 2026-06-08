//go:build system_test

package system

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	p2psqlite "github.com/LumeraProtocol/supernode/v2/p2p/kademlia/store/sqlite"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/action"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	storagechallenge "github.com/LumeraProtocol/supernode/v2/supernode/storage_challenge"
	"github.com/btcsuite/btcutil/base58"
	"github.com/stretchr/testify/require"
)

// TestLEP6NegativePerCaseCorruptionMatrix turns the manual local-devnet Phase 2D
// corruption matrix into real-binary system coverage. A real lumerad plus three
// real supernode binaries create/finalize the CASCADE action and persist the
// artifacts in the production P2P SQLite store. The assertions then mutate those
// real artifact rows and exercise the same LEP-6 P2P artifact reader that the
// storage-challenge gRPC handler uses before building INVALID_TRANSCRIPT proof
// results.
func TestLEP6NegativePerCaseCorruptionMatrix(t *testing.T) {
	fixture := setupLEP6RuntimeCascadeFixture(t)
	artifacts := resolveLEP6MatrixArtifacts(t, fixture.ctx, fixture.lumeraClient, fixture.actionID, fixture.nodes)

	t.Run("missing symbol row", func(t *testing.T) {
		withDeletedArtifact(t, fixture.ctx, artifacts.symbol.store, artifacts.symbol.key, artifacts.symbol.original)
		assertLEP6ArtifactReadFails(t, fixture.ctx, artifacts.symbol, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, "no rows in result set")
	})

	t.Run("wrong-size symbol row", func(t *testing.T) {
		bad := []byte("lep6-wrong-size-symbol")
		withCorruptedArtifact(t, fixture.ctx, artifacts.symbol.store, artifacts.symbol.key, artifacts.symbol.original, bad)
		assertLEP6ArtifactReadFails(t, fixture.ctx, artifacts.symbol, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, "content hash mismatch")
	})

	t.Run("missing index row", func(t *testing.T) {
		withDeletedArtifact(t, fixture.ctx, artifacts.index.store, artifacts.index.key, artifacts.index.original)
		assertLEP6ArtifactReadFails(t, fixture.ctx, artifacts.index, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, "no rows in result set")
	})

	t.Run("corrupt index row", func(t *testing.T) {
		bad := append([]byte(nil), artifacts.index.original...)
		require.NotEmpty(t, bad)
		bad[0] ^= 0xff
		withCorruptedArtifact(t, fixture.ctx, artifacts.index.store, artifacts.index.key, artifacts.index.original, bad)
		assertLEP6ArtifactReadFails(t, fixture.ctx, artifacts.index, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, "content hash mismatch")
	})

	downloadAndAssertCascadeBytes(t, fixture.ctx, fixture.actionClient, fixture.actionID, fixture.userAddress, t.TempDir(), fixture.originalData, fixture.originalHash)
}

type lep6RuntimeCascadeFixture struct {
	ctx          context.Context
	cli          *LumeradCli
	lumeraClient lumera.Client
	actionClient action.Client
	actionID     string
	userAddress  string
	nodes        []testNodeIdentity
	originalData []byte
	originalHash [32]byte
}

func setupLEP6RuntimeCascadeFixture(t *testing.T) lep6RuntimeCascadeFixture {
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

	recoveredAddress := cli.GetKeyAddr(testKeyName)
	require.Equal(t, expectedAddress, recoveredAddress)
	userAddress := cli.GetKeyAddr(userKeyName)
	cli.FundAddress(recoveredAddress, fundAmount)
	cli.FundAddress(userAddress, fundAmount)
	sut.AwaitNextBlock(t)

	t.Cleanup(restoreLEP6SupernodeFixturesAfterMatrix(t))
	cmds := StartLEP6Supernodes(t)
	t.Cleanup(func() { StopAllSupernodes(cmds) })
	time.Sleep(40 * time.Second) // allow supernode P2P/DHT routing to settle before upload

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
	t.Cleanup(func() { lumeraClient.Close() })

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
	_ = requireFinalizedCascadeArtifactCounts(t, ctx, lumeraClient, actionID)
	downloadAndAssertCascadeBytes(t, ctx, actionClient, actionID, userAddress, t.TempDir(), originalData, originalHash)

	return lep6RuntimeCascadeFixture{
		ctx:          ctx,
		cli:          cli,
		lumeraClient: lumeraClient,
		actionClient: actionClient,
		actionID:     actionID,
		userAddress:  userAddress,
		nodes:        []testNodeIdentity{n0, n1, n2},
		originalData: originalData,
		originalHash: originalHash,
	}
}

type lep6ArtifactRef struct {
	node     testNodeIdentity
	store    *p2psqlite.Store
	key      string
	original []byte
}

type lep6MatrixArtifacts struct {
	index  lep6ArtifactRef
	symbol lep6ArtifactRef
}

func resolveLEP6MatrixArtifacts(t *testing.T, ctx context.Context, lc lumera.Client, actionID string, nodes []testNodeIdentity) lep6MatrixArtifacts {
	t.Helper()
	resp, err := lc.Action().GetAction(ctx, actionID)
	require.NoError(t, err)
	require.NotNil(t, resp.GetAction())
	meta, err := cascadekit.UnmarshalCascadeMetadata(resp.GetAction().Metadata)
	require.NoError(t, err)
	require.NotEmpty(t, meta.RqIdsIds, "finalized action must contain index artifact IDs")

	stores := make(map[string]*p2psqlite.Store, len(nodes))
	openStore := func(node testNodeIdentity) *p2psqlite.Store {
		if stores[node.accAddr] != nil {
			return stores[node.accAddr]
		}
		store := openLEP6P2PStore(t, ctx, node, nodes)
		stores[node.accAddr] = store
		return store
	}

	var indexRef lep6ArtifactRef
	var indexFile cascadekit.IndexFile
	for _, indexKey := range meta.RqIdsIds {
		for _, node := range nodes {
			store := openStore(node)
			b, err := retrieveLEP6Artifact(ctx, store, indexKey)
			if err != nil {
				continue
			}
			idx, err := cascadekit.ParseCompressedIndexFile(b)
			if err != nil {
				continue
			}
			indexRef = lep6ArtifactRef{node: node, store: store, key: indexKey, original: append([]byte(nil), b...)}
			indexFile = idx
			break
		}
		if indexRef.key != "" {
			break
		}
	}
	require.NotEmpty(t, indexRef.key, "at least one real supernode P2P DB must contain a local index artifact")
	require.NotEmpty(t, indexFile.LayoutIDs, "index artifact must reference layout artifacts")

	var symbolIDs []string
	for _, layoutKey := range indexFile.LayoutIDs {
		for _, node := range nodes {
			layoutBytes, err := retrieveLEP6Artifact(ctx, openStore(node), layoutKey)
			if err != nil {
				continue
			}
			layout, _, _, err := cascadekit.ParseRQMetadataFile(layoutBytes)
			if err != nil {
				continue
			}
			for _, block := range layout.Blocks {
				for _, symbolID := range block.Symbols {
					if strings.TrimSpace(symbolID) != "" {
						symbolIDs = append(symbolIDs, symbolID)
					}
				}
			}
			break
		}
		if len(symbolIDs) > 0 {
			break
		}
	}
	require.NotEmpty(t, symbolIDs, "real persisted layout metadata must expose symbol artifact IDs")

	var symbolRef lep6ArtifactRef
	for _, symbolKey := range symbolIDs {
		for _, node := range nodes {
			store := openStore(node)
			b, err := retrieveLEP6Artifact(ctx, store, symbolKey)
			if err != nil {
				continue
			}
			symbolRef = lep6ArtifactRef{node: node, store: store, key: symbolKey, original: append([]byte(nil), b...)}
			break
		}
		if symbolRef.key != "" {
			break
		}
	}
	require.NotEmpty(t, symbolRef.key, "at least one real supernode P2P DB must contain a local symbol artifact")

	t.Logf("LEP-6 matrix artifacts: index key=%s node=%s bytes=%d; symbol key=%s node=%s bytes=%d",
		indexRef.key, indexRef.node.nodeName, len(indexRef.original), symbolRef.key, symbolRef.node.nodeName, len(symbolRef.original))
	return lep6MatrixArtifacts{index: indexRef, symbol: symbolRef}
}

func restoreLEP6SupernodeFixturesAfterMatrix(t *testing.T) func() {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join(WorkDir, "..", ".."))
	require.NoError(t, err)
	return func() {
		cmd := exec.Command("bash", "tests/scripts/setup-supernodes.sh",
			"all",
			"supernode/main.go",
			"tests/system/supernode-lep6-data1", "tests/system/config.lep6-1.yml",
			"tests/system/supernode-lep6-data2", "tests/system/config.lep6-2.yml",
			"tests/system/supernode-lep6-data3", "tests/system/config.lep6-3.yml",
		)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "restore LEP-6 supernode fixtures after negative matrix: %s", string(out))
	}
}

func openLEP6P2PStore(t *testing.T, ctx context.Context, node testNodeIdentity, nodes []testNodeIdentity) *p2psqlite.Store {
	t.Helper()
	baseDir := dataDirForSupernodeAccount(t, node.accAddr, nodes...)
	storeDir := filepath.Join(baseDir, "data", "p2p")
	store, err := p2psqlite.NewStore(ctx, storeDir, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close(context.Background()) })
	return store
}

func retrieveLEP6Artifact(ctx context.Context, store *p2psqlite.Store, key string) ([]byte, error) {
	decoded := base58.Decode(key)
	if len(decoded) != 32 {
		return nil, fmt.Errorf("invalid base58 artifact key %q", key)
	}
	return store.Retrieve(ctx, decoded)
}

func withDeletedArtifact(t *testing.T, ctx context.Context, store *p2psqlite.Store, key string, original []byte) {
	t.Helper()
	decoded := base58.Decode(key)
	require.Len(t, decoded, 32)
	store.Delete(ctx, decoded)
	require.Eventually(t, func() bool {
		_, err := store.Retrieve(ctx, decoded)
		return err != nil
	}, 15*time.Second, 200*time.Millisecond)
	t.Cleanup(func() { restoreLEP6Artifact(t, ctx, store, key, original) })
}

func withCorruptedArtifact(t *testing.T, ctx context.Context, store *p2psqlite.Store, key string, original, corrupted []byte) {
	t.Helper()
	decoded := base58.Decode(key)
	require.Len(t, decoded, 32)
	require.NoError(t, store.Store(ctx, decoded, corrupted, 1, true))
	require.Eventually(t, func() bool {
		got, err := store.Retrieve(ctx, decoded)
		return err == nil && string(got) == string(corrupted)
	}, 15*time.Second, 200*time.Millisecond)
	t.Cleanup(func() { restoreLEP6Artifact(t, ctx, store, key, original) })
}

func restoreLEP6Artifact(t *testing.T, ctx context.Context, store *p2psqlite.Store, key string, original []byte) {
	t.Helper()
	decoded := base58.Decode(key)
	require.Len(t, decoded, 32)
	require.NoError(t, store.Store(ctx, decoded, original, 1, true))
	require.Eventually(t, func() bool {
		got, err := store.Retrieve(ctx, decoded)
		return err == nil && string(got) == string(original)
	}, 15*time.Second, 200*time.Millisecond)
}

func assertLEP6ArtifactReadFails(t *testing.T, ctx context.Context, artifact lep6ArtifactRef, class audittypes.StorageProofArtifactClass, want string) {
	t.Helper()
	reader := storagechallenge.NewP2PArtifactReader(&lep6StoreBackedP2P{store: artifact.store})
	_, err := reader.ReadArtifactRange(ctx, class, artifact.key, 0, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), want)
}

type lep6StoreBackedP2P struct{ store *p2psqlite.Store }

func (p *lep6StoreBackedP2P) Retrieve(ctx context.Context, key string, localOnly ...bool) ([]byte, error) {
	return retrieveLEP6Artifact(ctx, p.store, key)
}
func (p *lep6StoreBackedP2P) BatchRetrieve(context.Context, []string, int, string, ...bool) (map[string][]byte, error) {
	return nil, fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) BatchRetrieveStream(context.Context, []string, int32, string, func(string, []byte) error, ...bool) (int32, error) {
	return 0, fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) Store(context.Context, []byte, int) (string, error) {
	return "", fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) StoreBatch(context.Context, [][]byte, int, string) error {
	return fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) Delete(context.Context, string) error {
	return fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) Stats(context.Context) (*p2p.StatsSnapshot, error) {
	return nil, fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) NClosestNodes(context.Context, int, string, ...string) []string {
	return nil
}
func (p *lep6StoreBackedP2P) NClosestNodesWithIncludingNodeList(context.Context, int, string, []string, []string) []string {
	return nil
}
func (p *lep6StoreBackedP2P) LocalStore(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) DisableKey(context.Context, string) error { return nil }
func (p *lep6StoreBackedP2P) EnableKey(context.Context, string) error  { return nil }
func (p *lep6StoreBackedP2P) GetLocalKeys(context.Context, *time.Time, time.Time) ([]string, error) {
	return nil, fmt.Errorf("not implemented in LEP-6 matrix test fake")
}
func (p *lep6StoreBackedP2P) Run(context.Context) error { return nil }
