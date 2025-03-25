package system

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	CascadePb "github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/p2p"
	snClient "github.com/LumeraProtocol/supernode/supernode/node/supernode/client"

	"github.com/LumeraProtocol/supernode/p2p/kademlia"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	lumeraActionMod "github.com/LumeraProtocol/supernode/pkg/lumera/modules/action"
	lumeraNodeMod "github.com/LumeraProtocol/supernode/pkg/lumera/modules/node"
	lumeraSupernodeMod "github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	lumeraTxMod "github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/storage/rqstore"
	"github.com/LumeraProtocol/supernode/pkg/testutil"
	"github.com/LumeraProtocol/supernode/supernode/cmd"
	"github.com/LumeraProtocol/supernode/supernode/config"
	CascadeActionServer "github.com/LumeraProtocol/supernode/supernode/node/action/server/cascade"
	"github.com/LumeraProtocol/supernode/supernode/services/cascade"
	"github.com/LumeraProtocol/supernode/supernode/services/common"

	"github.com/LumeraProtocol/lumera/x/action/types"
	snTypes "github.com/LumeraProtocol/lumera/x/supernode/types"

	types1 "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	basePort    = 9000
	dataDirRoot = "./data/nodes"
)

func TestSingleSupernodeSetup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	t.Log("Setting up a single Supernode with P2P and Cascade services...")

	kr := testutil.CreateTestKeyring()

	// Create test accounts
	accountNames := make([]string, 0)
	numP2PNodes := kademlia.Alpha + 1
	for i := 0; i < numP2PNodes; i++ {
		accountNames = append(accountNames, fmt.Sprintf("supernode-%d", i))
	}
	accountAddresses := testutil.SetupTestAccounts(t, kr, accountNames)

	var bootstrapNodeAddr string
	req := &CascadePb.UploadInputDataRequest{
		ActionId: "test-action-id",
		Filename: "test_file.txt",
		DataHash: "abcdef1234567890abcdef1234567890",
		RqIc:     5,
		RqMax:    10,
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLumeraClient := setupMockLumeraClient(ctrl, req.ActionId, accountAddresses[0])

	p2pClients, rqStores := SetupTestP2PNodes(t, ctx, kr, mockLumeraClient, numP2PNodes, accountNames, accountAddresses)

	cascadeService := setupCascadeService(ctrl, mockLumeraClient, accountAddresses[0], p2pClients[0], rqStores[0])

	grpcAddr := fmt.Sprintf("127.0.0.1:%d", basePort+100)
	grpcServer := startSupernodeGRPCServer(t, cascadeService, grpcAddr)
	grpcClient := connectToSupernodeGRPC(t, grpcAddr)

	var err error
	supernodeConfig := getSupernodeConfig(0, accountNames, &bootstrapNodeAddr)
	supernode, err := cmd.NewSupernode(ctx, supernodeConfig, kr, p2pClients[0], rqStores[0], mockLumeraClient)
	require.NoError(t, err, "Failed to create supernode")

	go func() {
		cascadeService.Run(ctx)
	}()

	t.Log("Sending UploadInputData request to the Supernode...")

	client := grpcClient

	stream, err := client.Session(ctx)
	require.NoError(t, err, "should successfully create session")
	sessReq := &CascadePb.SessionRequest{
		IsPrimary: true,
	}

	err = stream.Send(sessReq)
	require.NoError(t, err, "should successfully send to stream")

	res, err := stream.Recv()
	require.NoError(t, err, "should successfully rcv session")

	ctx = snClient.ContextWithMDSessID(ctx, res.SessID)
	resp, err := client.UploadInputData(ctx, req)
	require.NoError(t, err, "Failed to upload input data at Supernode")
	require.True(t, resp.Success, "UploadInputData request at Supernode should succeed")

	t.Cleanup(func() {
		cleanup(t, supernode, grpcServer)
	})
}

// cleanup stops the supernode, GRPC server and cleans up data directories.
func cleanup(t *testing.T, supernode *cmd.Supernode, grpcServer *grpc.Server) {
	t.Log("Cleaning up supernode, gRPC server, and data directories...")

	if supernode != nil {
		err := supernode.Stop(context.Background())
		if err != nil {
			t.Logf("Failed to stop supernode: %v", err)
		}
	}
	if grpcServer != nil {
		grpcServer.Stop()
	}

	err := os.RemoveAll(dataDirRoot)
	if err != nil {
		t.Logf("Failed to remove data directory: %v", err)
	} else {
		t.Log("✅ Data directories cleaned up successfully.")
	}
}

func setupCascadeService(ctrl *gomock.Controller, lumeraClient lumera.Client, accountAddr string, p2pClient *p2p.P2P, rqStore *rqstore.SQLiteRQStore) *cascade.CascadeService {
	dataDir := filepath.Join(dataDirRoot, accountAddr)

	// fileStorage := fs.NewFileStorage(filepath.Join(dataDir, "storage"))
	mockRaptorQ := raptorq.NewMockRaptorQ(ctrl)
	mockRaptorQClient := raptorq.NewMockClientInterface(ctrl)

	mockRaptorQ.EXPECT().GenRQIdentifiersFiles(
		gomock.Any(), // ctx
		gomock.Any(), // fields
		gomock.Any(), // data
		gomock.Any(), // operationBlockHash
		gomock.Any(), // pastelID
		gomock.Any(), // rqMax
	).Return(
		uint32(12345),                               // RQIDsIc
		[]string{"id1", "id2", "id3"},               // RQIDs
		[][]byte{[]byte("first"), []byte("second")}, // RQIDsFiles
		[]byte("some_bytes_for_file"),               // RQIDsFile
		raptorq.EncoderParameters{Oti: []byte("some_encoded_value")},
		[]byte(accountAddr),
		nil,
	).AnyTimes()

	return cascade.NewCascadeService(
		&cascade.Config{
			Config: common.Config{
				SupernodeAccountAddress: accountAddr,
			},
			RaptorQServiceAddress: "",
			RqFilesDir:            filepath.Join(dataDir, "rqfiles"),
			NumberConnectedNodes:  1,
		},
		lumeraClient,
		nil,
		nil, // FIXME
		*p2pClient,
		mockRaptorQ,
		mockRaptorQClient,
		rqStore,
	)
}

// Starts gRPC server for a Supernode
func startSupernodeGRPCServer(t *testing.T, service *cascade.CascadeService, address string) *grpc.Server {
	t.Helper()
	grpcServer := grpc.NewServer()
	CascadePb.RegisterCascadeServiceServer(grpcServer,
		CascadeActionServer.NewCascadeActionServer(service),
	)

	listener, err := net.Listen("tcp", address)
	require.NoError(t, err, fmt.Sprintf("Failed to start gRPC listener on %s", address))

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Logf("gRPC server stopped: %v", err)
		}
	}()
	time.Sleep(2 * time.Second)
	return grpcServer
}

func connectToSupernodeGRPC(t *testing.T, address string) CascadePb.CascadeServiceClient {
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err, fmt.Sprintf("Failed to connect to gRPC server at %s", address))
	return CascadePb.NewCascadeServiceClient(conn)
}

func setupMockLumeraClient(ctrl *gomock.Controller, actionID string, accAddress string) lumera.Client {
	mockLumeraClient := lumera.NewMockClient(ctrl)
	mockActionClient := lumeraActionMod.NewMockModule(ctrl)
	mockNodeClient := lumeraNodeMod.NewMockModule(ctrl)
	mockTxClient := lumeraTxMod.NewMockModule(ctrl)
	mockSupernodeClient := lumeraSupernodeMod.NewMockModule(ctrl)

	mockLumeraClient.EXPECT().Action().Return(mockActionClient).AnyTimes()
	mockLumeraClient.EXPECT().Node().Return(mockNodeClient).AnyTimes()
	mockLumeraClient.EXPECT().Tx().Return(mockTxClient).AnyTimes()
	mockLumeraClient.EXPECT().SuperNode().Return(mockSupernodeClient).AnyTimes()

	mockActionClient.EXPECT().GetAction(gomock.Any(), actionID).Return(&types.QueryGetActionResponse{
		Action: &types.Action{
			ActionID:    actionID,
			Creator:     "test-action-creator",
			BlockHeight: 100,
			Metadata: &types.Metadata{
				MetadataType: &types.Metadata_CascadeMetadata{
					CascadeMetadata: &types.CascadeMetadata{
						DataHash: "abcdef1234567890abcdef1234567890",
						FileName: "test_file.txt",
						RqMax:    10,
						RqIc:     5,
					},
				},
			},
			State: types.ActionStateApproved,
		},
	}, nil).AnyTimes()

	mockNodeClient.EXPECT().GetLatestBlock(gomock.Any()).Return(&cmtservice.GetLatestBlockResponse{
		BlockId: &types1.BlockID{
			Hash: []byte("latestblockhash"),
		},
		SdkBlock: &cmtservice.Block{
			Header: cmtservice.Header{
				Height: 100,
			},
		},
	}, nil).AnyTimes()

	mockSupernodeClient.EXPECT().GetTopSuperNodesForBlock(gomock.Any(), gomock.Any()).Return(&snTypes.QueryGetTopSuperNodesForBlockResponse{
		Supernodes: []*snTypes.SuperNode{
			{
				SupernodeAccount: accAddress,
			},
		},
	}, nil).AnyTimes()

	return mockLumeraClient
}

func getSupernodeConfig(i int, accountNames []string, bootstrapNodeAddr *string) *config.Config {
	bootstrapNodes := ""
	if i > 0 {
		bootstrapNodes = *bootstrapNodeAddr
	} else {
		*bootstrapNodeAddr = fmt.Sprintf("127.0.0.1:%s@%d", accountNames[i], basePort+i)
	}

	return &config.Config{
		P2PConfig: config.P2PConfig{
			ListenAddress:  "127.0.0.1",
			Port:           uint16(basePort + i),
			DataDir:        filepath.Join(dataDirRoot, accountNames[i]),
			BootstrapNodes: bootstrapNodes,
		},
		SupernodeConfig: config.SupernodeConfig{
			KeyName: accountNames[i],
		},
	}
}

// SetupTestP2PNodes now supports multiple nodes
func SetupTestP2PNodes(t *testing.T, ctx context.Context, kr keyring.Keyring,
	lumeraC lumera.Client, numP2PNodes int, accountNames []string, accountAddresses []string,
) ([]*p2p.P2P, []*rqstore.SQLiteRQStore) {
	var services []*p2p.P2P
	var rqStores []*rqstore.SQLiteRQStore

	// Setup node addresses and their corresponding Lumera IDs
	var nodeConfigs ltc.LumeraAddresses
	for i := 0; i < numP2PNodes; i++ {
		nodeConfigs = append(nodeConfigs, ltc.LumeraAddress{
			Identity: accountAddresses[i],
			Host:     "127.0.0.1",
			Port:     uint16(9000 + i),
		})
	}

	for i, config := range nodeConfigs {
		dataDir := filepath.Join(dataDirRoot, accountNames[i])
		require.NoError(t, os.MkdirAll(dataDir, 0755), "Failed to create data directory for node %d", i)

		// Get all previous addresses to use as bootstrap addresses
		bootstrapAddresses := make([]string, i)
		for j := 0; j < i; j++ {
			bootstrapAddresses[j] = nodeConfigs[j].String()
		}

		p2pConfig := &p2p.Config{
			ListenAddress:  config.Host,
			Port:           config.Port,
			DataDir:        dataDir,
			ID:             config.Identity,
			BootstrapNodes: strings.Join(bootstrapAddresses, ","),
		}

		rqStoreFile := filepath.Join(dataDir, "rqstore.db")
		require.NoError(t, os.MkdirAll(filepath.Dir(rqStoreFile), 0755), "Failed to create rqstore directory for node %d", i)

		rqStore, err := rqstore.NewSQLiteRQStore(rqStoreFile)
		require.NoError(t, err, "Failed to create rqstore for node %d", i)

		p2pClient, err := p2p.New(ctx, p2pConfig, lumeraC, kr, rqStore, nil, nil)
		require.NoError(t, err, "Failed to create P2P client for node %d", i)

		go func() {
			if err := p2pClient.Run(ctx); err != nil && err != context.Canceled {
				t.Logf("P2P service for node %d failed: %v", i, err)
			}
		}()

		services = append(services, &p2pClient)
		rqStores = append(rqStores, rqStore)

		// Give nodes time to start up and connect
		time.Sleep(2 * time.Second)
	}

	// Give extra time for all nodes to connect
	time.Sleep(3 * time.Second)

	return services, rqStores
}
