package self_healing

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	lumeraclient "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/v2/pkg/testutil"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type staticKeyExchangerValidator struct {
	supernodes map[string]struct{}
}

func (v *staticKeyExchangerValidator) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	_ = ctx
	return &authtypes.QueryAccountInfoResponse{
		Info: &authtypes.BaseAccount{Address: addr},
	}, nil
}

func (v *staticKeyExchangerValidator) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*sntypes.SuperNode, error) {
	_ = ctx
	if _, ok := v.supernodes[address]; !ok {
		return nil, fmt.Errorf("unknown supernode: %s", address)
	}
	return &sntypes.SuperNode{SupernodeAccount: address}, nil
}

type secureSelfHealingFixture struct {
	recipientID     string
	challengerID    string
	intruderID      string
	serverAddr      string
	fileKey         string
	actionID        string
	expectedHashHex string

	challengerCreds credentials.TransportCredentials
	intruderCreds   credentials.TransportCredentials

	cleanup func()
}

func setupSecureSelfHealingFixture(t *testing.T) *secureSelfHealingFixture {
	t.Helper()

	recipientKR := testutil.CreateTestKeyring()
	challengerKR := testutil.CreateTestKeyring()
	intruderKR := testutil.CreateTestKeyring()

	recipientID := testutil.SetupTestAccounts(t, recipientKR, []string{"recipient-secure"})[0].Address
	challengerID := testutil.SetupTestAccounts(t, challengerKR, []string{"challenger-secure"})[0].Address
	intruderID := testutil.SetupTestAccounts(t, intruderKR, []string{"intruder-secure"})[0].Address

	validator := &staticKeyExchangerValidator{
		supernodes: map[string]struct{}{
			recipientID:  {},
			challengerID: {},
			intruderID:   {},
		},
	}

	serverCreds := mustNewServerCreds(t, recipientKR, recipientID, validator)
	challengerCreds := mustNewClientCreds(t, challengerKR, challengerID, validator)
	intruderCreds := mustNewClientCreds(t, intruderKR, intruderID, validator)

	const fileKey = "secure-file-key-1"
	const actionID = "secure-action-1"
	payload := []byte("secure-self-healing-payload")
	expectedHashHex := blake3Hex(payload)

	recipientP2P := newFakeP2P()
	recipientP2P.local[fileKey] = payload

	lumeraClient, lumeraCleanup := mockLumeraActionLookup(t, fileKey, actionID, expectedHashHex)

	serverAddr, stopServer := startSecureSelfHealingServer(
		t,
		recipientID,
		recipientP2P,
		lumeraClient,
		SecurityConfig{
			EnforceAuthenticatedCaller: true,
			AllowUnauthenticatedCaller: false,
		},
		serverCreds,
	)

	cleanup := func() {
		stopServer()
		lumeraCleanup()
	}

	return &secureSelfHealingFixture{
		recipientID:     recipientID,
		challengerID:    challengerID,
		intruderID:      intruderID,
		serverAddr:      serverAddr,
		fileKey:         fileKey,
		actionID:        actionID,
		expectedHashHex: expectedHashHex,
		challengerCreds: challengerCreds,
		intruderCreds:   intruderCreds,
		cleanup:         cleanup,
	}
}

func mustNewServerCreds(t *testing.T, kr keyring.Keyring, identity string, validator securekeyx.KeyExchangerValidator) credentials.TransportCredentials {
	t.Helper()
	creds, err := ltc.NewServerCreds(&ltc.ServerOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       kr,
			LocalIdentity: identity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	require.NoError(t, err)
	return creds
}

func mustNewClientCreds(t *testing.T, kr keyring.Keyring, identity string, validator securekeyx.KeyExchangerValidator) credentials.TransportCredentials {
	t.Helper()
	creds, err := ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       kr,
			LocalIdentity: identity,
			PeerType:      securekeyx.Supernode,
			Validator:     validator,
		},
	})
	require.NoError(t, err)
	return creds
}

func startSecureSelfHealingServer(
	t *testing.T,
	identity string,
	p2p *fakeP2P,
	lumeraClient lumeraclient.Client,
	securityCfg SecurityConfig,
	serverCreds credentials.TransportCredentials,
) (string, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer(grpc.Creds(serverCreds))
	supernode.RegisterSelfHealingServiceServer(
		grpcServer,
		NewServer(identity, p2p, lumeraClient, nil, securityCfg),
	)

	go func() { _ = grpcServer.Serve(lis) }()

	stop := func() {
		grpcServer.Stop()
		_ = lis.Close()
	}
	return lis.Addr().String(), stop
}

func dialSecureSelfHealingClient(t *testing.T, creds credentials.TransportCredentials, remoteIdentity, addr string) (*grpc.ClientConn, error) {
	t.Helper()

	lumeraTC, ok := creds.(*ltc.LumeraTC)
	require.True(t, ok, "expected *credentials.LumeraTC, got %T", creds)
	lumeraTC.SetRemoteIdentity(strings.TrimSpace(remoteIdentity))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	return grpc.DialContext(
		ctx,
		addr,
		grpc.WithBlock(),
		grpc.WithTransportCredentials(creds),
	)
}

func TestSelfHealingSecureRPC_StrictAuthenticatedRequestAccepted(t *testing.T) {
	f := setupSecureSelfHealingFixture(t)
	defer f.cleanup()

	conn, err := dialSecureSelfHealingClient(t, f.challengerCreds, f.recipientID, f.serverAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := supernode.NewSelfHealingServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
		ChallengeId:  "secure-ch-ok",
		EpochId:      1,
		FileKey:      f.fileKey,
		ChallengerId: f.challengerID,
		RecipientId:  f.recipientID,
		ActionId:     f.actionID,
	})
	require.NoError(t, err)
	require.True(t, resp.Accepted, "request rejected: %s", resp.Error)
	require.True(t, strings.EqualFold(resp.ReconstructedHashHex, f.expectedHashHex))
}

func TestSelfHealingSecureRPC_StrictCallerIdentityMismatchRejected(t *testing.T) {
	f := setupSecureSelfHealingFixture(t)
	defer f.cleanup()

	conn, err := dialSecureSelfHealingClient(t, f.intruderCreds, f.recipientID, f.serverAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := supernode.NewSelfHealingServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
		ChallengeId:  "secure-ch-mismatch",
		EpochId:      1,
		FileKey:      f.fileKey,
		ChallengerId: f.challengerID, // caller is intruderID, so mismatch must be rejected
		RecipientId:  f.recipientID,
		ActionId:     f.actionID,
	})
	require.NoError(t, err)
	require.False(t, resp.Accepted)
	require.Contains(t, strings.ToLower(resp.Error), "caller identity mismatch")
}

func TestSelfHealingSecureRPC_RejectsInsecureTransport(t *testing.T) {
	f := setupSecureSelfHealingFixture(t)
	defer f.cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Insecure client should not be able to establish RPC with secure ALTS server.
	conn, err := grpc.DialContext(
		ctx,
		f.serverAddr,
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err == nil {
		defer func() { _ = conn.Close() }()
		client := supernode.NewSelfHealingServiceClient(conn)
		_, callErr := client.RequestSelfHealing(ctx, &supernode.RequestSelfHealingRequest{
			ChallengeId:  "secure-ch-insecure",
			EpochId:      1,
			FileKey:      f.fileKey,
			ChallengerId: f.challengerID,
			RecipientId:  f.recipientID,
			ActionId:     f.actionID,
		})
		require.Error(t, callErr)
		return
	}

	msg := strings.ToLower(err.Error())
	require.True(
		t,
		strings.Contains(msg, "handshake") ||
			strings.Contains(msg, "authentication") ||
			strings.Contains(msg, "transport") ||
			strings.Contains(msg, "credentials"),
		"unexpected dial error: %v", err,
	)
}
