// Generate Go code with:
//go:generate protoc --proto_path=../../../proto/tests --go_out=../../../gen --go-grpc_out=../../../gen grpc_test_service.proto

package securegrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
	"regexp"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	pb "github.com/LumeraProtocol/supernode/gen/supernode/tests/integration/securegrpc"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/testutil"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/pkg/net/grpc/server"
)

func waitForServerReady(address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", address)
		if err == nil {
			conn.Close()
			fmt.Println("Server is ready and accepting connections.")
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
	conn.RegisterALTSRecordProtocols()
	defer conn.UnregisterALTSRecordProtocols()

	// Set gRPC log level
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(os.Stdout, os.Stderr, os.Stderr, 2))

	// Create test kr
	kr := testutil.CreateTestKeyring()

	// Create test accounts
	accountNames := []string{"test-server", "test-client"}
	addresses := testutil.SetupTestAccounts(t, kr, accountNames)
	serverAddress := addresses[0]
	clientAddress := addresses[1]

	// Create server credentials
	serverCreds, err := credentials.NewServerCreds(&credentials.ServerOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       kr,
			LocalIdentity: serverAddress,
			PeerType:      securekeyx.Supernode,
		},
	})
	require.NoError(t, err, "failed to create server credentials")

	// Create client credentials
	clientCreds, err := credentials.NewClientCreds(&credentials.ClientOptions{
		CommonOptions: credentials.CommonOptions{
			Keyring:       kr,
			LocalIdentity: clientAddress,
			PeerType:      securekeyx.Simplenode,
		},
		RemoteIdentity: serverAddress,
	})
	require.NoError(t, err, "failed to create client credentials")

	// Get free port for gRPC server
	listenPort, err := testutil.GetFreePortInRange(50051, 50160) // Ensure port is free
	require.NoError(t, err, "failed to get free port")
	grpcServerAddress := fmt.Sprintf("localhost:%d", listenPort)

	// Start gRPC server
	grpcServer := server.NewServer(serverCreds)
	pb.RegisterTestServiceServer(grpcServer, &TestServiceImpl{})

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverOptions := server.DefaultServerOptions()
	serverOptions.MaxConnectionIdle = time.Minute
	serverOptions.MaxConnectionAge = 5 * time.Minute

	go func() {
		err := grpcServer.Serve(ctx, grpcServerAddress, serverOptions)
		require.NoError(t, err, "server failed to start")
	}()
	err = waitForServerReady(grpcServerAddress, 300*time.Second)
	require.NoError(t, err, "server did not become ready in time")
	// Set health status to SERVING only after the server has successfully started
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	clientOptions := client.DefaultClientOptions()
	clientOptions.ConnWaitTime = 10 * time.Second
	clientOptions.EnableRetries = false

	// Create gRPC client and connect
	grpcClient := client.NewClient(clientCreds)
	conn, err := grpcClient.Connect(ctx, grpcServerAddress, clientOptions)
	require.NoError(t, err, "client failed to connect to server")
	defer conn.Close()

	client := pb.NewTestServiceClient(conn)
	resp, err := client.TestMethod(ctx, &pb.TestRequest{Message: "Hello Lumera Server! I'm [TestClient]!"})
	require.NoError(t, err, "failed to send request")
	require.Equal(t, "Hello, TestClient", resp.Response, "unexpected response from server")

	// Gracefully close client connection
	err = conn.Close()
	require.NoError(t, err, "failed to close client connection")

	// Gracefully stop server
	err = grpcServer.Stop(5 * time.Second)
	require.NoError(t, err, "failed to stop server")
}
