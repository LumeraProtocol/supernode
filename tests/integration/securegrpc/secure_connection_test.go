// Generate Go code with:
//go:generate protoc --proto_path=../../../proto/tests --go_out=../../../gen --go-grpc_out=../../../gen grpc_test_service.proto

package securegrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	pb "github.com/LumeraProtocol/supernode/gen/supernode/tests/integration/securegrpc"
	ltc "github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/server"
	snkeyring "github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/pkg/testutil"
)

func waitForServerReady(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", address)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not become ready in time")
}

type TestServiceImpl struct {
	pb.UnimplementedTestServiceServer // Embedding ensures forward compatibility
}

func (s *TestServiceImpl) TestMethod(ctx context.Context, req *pb.TestRequest) (*pb.TestResponse, error) {
	// request is "Hello Lumera Server! I'm [TestClient]!"
	re := regexp.MustCompile(`\[(.*?)\]`)
	matches := re.FindStringSubmatch(req.Message)

	clientName := "Unknown Client"
	if len(matches) > 1 {
		clientName = matches[1]
	}

	return &pb.TestResponse{Response: "Hello, " + clientName}, nil
}

func TestSecureGRPCConnection(t *testing.T) {
	snkeyring.InitSDKConfig()

	conn.RegisterALTSRecordProtocols()
	defer conn.UnregisterALTSRecordProtocols()

	// Set gRPC log level
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(os.Stdout, os.Stderr, os.Stderr, 2))

	// Create test keyrings
	clientKr := testutil.CreateTestKeyring()
	serverKr := testutil.CreateTestKeyring()

	// Create test accounts
	testAccounts := testutil.SetupTestAccounts(t, clientKr, []string{"test-client"})
	clientAddress := testAccounts[0].Address

	testAccounts = testutil.SetupTestAccounts(t, serverKr, []string{"test-server"})
	serverAddress := testAccounts[0].Address

	// Create server credentials
	serverCreds, err := ltc.NewServerCreds(&ltc.ServerOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       serverKr,
			LocalIdentity: serverAddress,
			PeerType:      securekeyx.Supernode,
		},
	})
	require.NoError(t, err, "failed to create server credentials")

	// Create client credentials
	clientCreds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       clientKr,
			LocalIdentity: clientAddress,
			PeerType:      securekeyx.Simplenode,
		},
	})
	require.NoError(t, err, "failed to create client credentials")

	// Get free port for gRPC server
	listenPort, err := testutil.GetFreePortInRange(50051, 50160) // Ensure port is free
	require.NoError(t, err, "failed to get free port")
	grpcServerAddress := fmt.Sprintf("localhost:%d", listenPort)

	// Start gRPC server
	grpcServer := server.NewServer("Integration_Test_Server", serverCreds)
	pb.RegisterTestServiceServer(grpcServer, &TestServiceImpl{})

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	serverOptions := server.DefaultServerOptions()
	serverOptions.MaxConnectionIdle = time.Minute
	serverOptions.MaxConnectionAge = 5 * time.Minute

	go func() {
		err := grpcServer.Serve(serverCtx, grpcServerAddress, serverOptions)
		require.NoError(t, err, "server failed to start")
	}()
	err = waitForServerReady(grpcServerAddress, 300*time.Second)
	require.NoError(t, err, "server did not become ready in time")
	// Set health status to SERVING only after the server has successfully started
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	clientCtx, clientCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer clientCancel()

	clientOptions := client.DefaultClientOptions()
	clientOptions.ConnWaitTime = 10 * time.Second
	clientOptions.EnableRetries = false

	// Create gRPC client and connect
	grpcClient := client.NewClient(clientCreds)
	addressWithIdentity := ltc.FormatAddressWithIdentity(serverAddress, grpcServerAddress)
	conn, err := grpcClient.Connect(clientCtx, addressWithIdentity, clientOptions)
	require.NoError(t, err, "client failed to connect to server")
	defer conn.Close()

	client := pb.NewTestServiceClient(conn)
	resp, err := client.TestMethod(clientCtx, &pb.TestRequest{Message: "Hello Lumera Server! I'm [TestClient]!"})
	require.NoError(t, err, "failed to send request")
	require.Equal(t, "Hello, TestClient", resp.Response, "unexpected response from server")

	// Gracefully close client connection
	err = conn.Close()
	require.NoError(t, err, "failed to close client connection")

	// Gracefully stop server
	err = grpcServer.Stop(5 * time.Second)
	require.NoError(t, err, "failed to stop server")
}
