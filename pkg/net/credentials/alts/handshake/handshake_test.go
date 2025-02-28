package handshake

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	lumeraidmocks "github.com/LumeraProtocol/lumera/x/lumeraid/mocks"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	lumeraidtypes "github.com/LumeraProtocol/lumera/x/lumeraid/types"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/testutil"
	. "github.com/LumeraProtocol/supernode/pkg/testutil"
)

const defaultTestTimeout = 100 * time.Second

var stat testutil.Stats

func init() {
	conn.RegisterALTSRecordProtocols()
}

type hsInterceptor struct {
	ctrl 					     *gomock.Controller
	ke                           *lumeraidmocks.MockKeyExchanger
	conn                         net.Conn
	hs                           *secureHandshaker
	timeout                      time.Duration
	remoteAddr                   string
	savedSendHandshakeMessage    SendHandshakeMessageFunc
	savedReceiveHandshakeMessage ReceiveHandshakeMessageFunc
	savedParseHandshakeMessage   ParseHandshakeMessageFunc
	savedExpandKey               ExpandKeyFunc
}

func newHSInterceptor(t *testing.T, remoteAddr string, side Side, opts interface{}) *hsInterceptor {
	ctrl := gomock.NewController(t)
	ke := lumeraidmocks.NewMockKeyExchanger(ctrl)
	conn, _ := net.Pipe()

	defaultHandshakeTimeout := 10 * time.Second
	hs := newHandshaker(ke, conn, remoteAddr, side, defaultHandshakeTimeout, opts)

	interceptor := &hsInterceptor{
		ctrl:       ctrl,
		ke:         ke,
		conn:       conn,
		hs:         hs,
		timeout:    defaultHandshakeTimeout,
		remoteAddr: remoteAddr,
	}

	return interceptor
}

const (
	// constants for testing
	testRemoteAddr = "remote"
	testLocalAddr  = "local"
)

var (
	testHandshakeMessage  = []byte("handshake message")
	testSignature         = []byte("signature")
	testSharedSecret      = []byte("shared secret")
	testHandshakeRequest  = []byte("handshake request")
	testHandshakeResponse = []byte("handshake response")
)

func newHSClientInterceptor(t *testing.T, opts interface{}) *hsInterceptor {
	return newHSInterceptor(t, testRemoteAddr, ClientSide, opts)
}

func newHSServerInterceptor(t *testing.T, opts interface{}) *hsInterceptor {
	return newHSInterceptor(t, testRemoteAddr, ServerSide, opts)
}

func (h *hsInterceptor) setHandshakeTimeout(timeout time.Duration) {
	h.timeout = timeout
	h.hs.timeout = timeout
}

func (h *hsInterceptor) mockCreateRequestSuccess() {
	h.ke.EXPECT().CreateRequest(h.remoteAddr).Return(testHandshakeMessage, testSignature, nil)
}

func (h *hsInterceptor) mockComputeSharedSecret(fn func([]byte, []byte) ([]byte, error)) {
	var handshakeBytes []byte
	if h.hs.side == ClientSide {
		handshakeBytes = testHandshakeResponse
	} else {
		handshakeBytes = testHandshakeRequest
	}
	h.ke.EXPECT().ComputeSharedSecret(handshakeBytes, testSignature).DoAndReturn(fn)
}

func (h *hsInterceptor) mockComputeSharedSecretSuccess() {
	var handshakeBytes []byte
	if h.hs.side == ClientSide {
		handshakeBytes = testHandshakeResponse
	} else {
		handshakeBytes = testHandshakeRequest
	}
	h.ke.EXPECT().ComputeSharedSecret(handshakeBytes, testSignature).Return(testSharedSecret, nil)
}

func (h *hsInterceptor) mockLocalAddress() {
	h.ke.EXPECT().LocalAddress().Return(testLocalAddr)
}

func (h *hsInterceptor) overrideSendHandshakeMessage(fn SendHandshakeMessageFunc) {
	h.savedSendHandshakeMessage = SendHandshakeMessage
	SendHandshakeMessage = fn
}

func (h *hsInterceptor) overrideSendHandshakeMessageSuccess() {
	h.savedSendHandshakeMessage = SendHandshakeMessage
	SendHandshakeMessage = func(conn net.Conn, handshakeBytes, signature []byte) error {
		return nil
	}
}

func (h *hsInterceptor) overrideReceiveHandshakeMessage(fn ReceiveHandshakeMessageFunc) {
	h.savedReceiveHandshakeMessage = ReceiveHandshakeMessage
	ReceiveHandshakeMessage = fn
}

func (h *hsInterceptor) overrideReceiveHandshakeMessageSuccess() {
	h.savedReceiveHandshakeMessage = ReceiveHandshakeMessage
	ReceiveHandshakeMessage = func(conn net.Conn) ([]byte, []byte, error) {
		return testHandshakeMessage, testSignature, nil
	}
}

func (h *hsInterceptor) overrideParseHandshakeMessage(fn ParseHandshakeMessageFunc) {
	h.savedParseHandshakeMessage = ParseHandshakeMessage
	ParseHandshakeMessage = fn
}

func (h *hsInterceptor) overrideParseHandshakeMessageSuccess(address string, peerType securekeyx.PeerType) {
	h.savedParseHandshakeMessage = ParseHandshakeMessage
	ParseHandshakeMessage = func([]byte) (*lumeraidtypes.HandshakeInfo, error) {
		return &lumeraidtypes.HandshakeInfo{
			Address:   address,
			PeerType:  int32(peerType),
			PublicKey: []byte("public key"),
			Curve:     "P-256",
		}, nil
	}
}

func (h *hsInterceptor) overrideReadRequestWithTimeout(fn ReadRequestWithTimeoutFunc) {
	h.hs.readRequestWithTimeoutFn = fn
}

func (h *hsInterceptor) overrideReadRequestWithTimeoutSuccess() {
	h.hs.readRequestWithTimeoutFn = func(ctx context.Context) ([]byte, []byte, error) {
		return testHandshakeRequest, testSignature, nil
	}
}

func (h *hsInterceptor) overrideReadResponseWithTimeout(fn ReadResponseWithTimeoutFunc) {
	h.hs.readResponseWithTimeoutFn = fn
}

func (h *hsInterceptor) overrideReadResponseWithTimeoutSuccess() {
	h.hs.readResponseWithTimeoutFn = func(ctx context.Context, lastWrite time.Time) ([]byte, []byte, error) {
		return testHandshakeResponse, testSignature, nil
	}
}

func (h *hsInterceptor) overrideExpandKey(fn ExpandKeyFunc) {
	h.savedExpandKey = ExpandKey
	ExpandKey = fn
}

func (h *hsInterceptor) overrideExpandKeySuccess() {
	h.savedExpandKey = ExpandKey
	ExpandKey = func([]byte, string, []byte) ([]byte, error) {
		return []byte("expanded key"), nil
	}
}

func (h *hsInterceptor) cleanup() {
	if h.savedSendHandshakeMessage != nil {
		SendHandshakeMessage = h.savedSendHandshakeMessage
	}
	if h.savedReceiveHandshakeMessage != nil {
		ReceiveHandshakeMessage = h.savedReceiveHandshakeMessage
	}
	if h.savedParseHandshakeMessage != nil {
		ParseHandshakeMessage = h.savedParseHandshakeMessage
	}
	if h.savedExpandKey != nil {
		ExpandKey = h.savedExpandKey
	}
	h.conn.Close()
}

func TestHandshakerConcurrentHandshakes(t *testing.T) {
	kr := CreateTestKeyring()

	testCases := []struct {
		name          string
		numHandshakes int
		readLatency   time.Duration
		newConnWait   bool // Wait in NewConn to simulate slow connections (for concurrent handshakes tests)
		expectSuccess bool
	}{
		{
			name:          "Single handshake",
			numHandshakes: 1,
			newConnWait:   false,
			expectSuccess: true,
		},
		{
			name:          "Multiple concurrent handshakes within limit",
			numHandshakes: maxConcurrentHandshakes / 2,
			newConnWait:   true,
			expectSuccess: true,
		},
		{
			name:          "Maximum concurrent handshakes",
			numHandshakes: maxConcurrentHandshakes,
			newConnWait:   true,
			expectSuccess: true,
		},
		{
			name:          "Exceeding concurrent handshakes limit",
			numHandshakes: maxConcurrentHandshakes + 5,
			newConnWait:   true,
			expectSuccess: false,
		},
		{
			name:          "Handshakes with latency",
			numHandshakes: maxConcurrentHandshakes / 2,
			readLatency:   100 * time.Millisecond,
			newConnWait:   false,
			expectSuccess: true,
		},
	}

	// store the original functions
	originalNewConn := conn.NewConn

	defer func() {
		// restore the original functions
		conn.NewConn = originalNewConn
	}()

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	shouldWaitInNewConn := false // Flag to control whether NewConn should wait

	// Channel to count completed handshakes
	var newConnCalledCh chan struct{}
	var newConnCalledCounter atomic.Uint64

	conn.NewConn = func(c net.Conn, side Side, recordProtocol string, key, protected []byte) (net.Conn, error) {
		counter := newConnCalledCounter.Add(1)
		// Only when we are simulating slow connections should we enforce the limit.
		// (For tests where newConnWait is false, we let every handshake go through.)
		if shouldWaitInNewConn && counter > uint64(maxConcurrentHandshakes*2) {
			// Extra handshake call: do not block, return error immediately.
			return nil, fmt.Errorf("concurrent handshake limit exceeded")
		}

		if shouldWaitInNewConn {
			mu.Lock()
			// Only allowed handshake calls signal that they’ve reached NewConn.
			newConnCalledCh <- struct{}{}
			cond.Wait()
			mu.Unlock()
		} else {
			newConnCalledCh <- struct{}{}
		}
		return originalNewConn(c, side, recordProtocol, key, protected)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stat.Reset()
			newConnCalledCounter.Store(0)

			newConnCalledCh = make(chan struct{}, tc.numHandshakes*2)

			// Channel to collect errors
			errChan := make(chan error, tc.numHandshakes*2)

			// Set whether NewConn should wait based on test case
			shouldWaitInNewConn = tc.newConnWait

			// Create handshake pairs
			for i := 0; i < tc.numHandshakes; i++ {
				accountClient := fmt.Sprintf("client-%d", i)
				accountServer := fmt.Sprintf("server-%d", i)
				addresses := SetupTestAccounts(t, kr, []string{accountClient, accountServer})

				clientAddr := addresses[0]
				serverAddr := addresses[1]
				clientKE := SetupTestKeyExchange(t, kr, clientAddr, securekeyx.Simplenode)
				serverKE := SetupTestKeyExchange(t, kr, serverAddr, securekeyx.Supernode)

				// Setup test pipes
				clientConn, serverConn := net.Pipe()

				if tc.readLatency > 0 {
					clientConn = testutil.NewTestConnWithReadLatency(clientConn, tc.readLatency)
					serverConn = testutil.NewTestConnWithReadLatency(serverConn, tc.readLatency)
				}

				// Create handshakers
				clientHS := NewClientHandshaker(clientKE, clientConn, serverAddr, nil)
				serverHS := NewServerHandshaker(serverKE, serverConn, nil)

				// client -> server handshake
				go func(accountName string) {
					defer func() {
						if r := recover(); r != nil {
							errChan <- fmt.Errorf("client panic: %v", r)
						}
					}()

					ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
					defer cancel()

					conn, authInfo, err := clientHS.ClientHandshake(ctx)
					if err == nil {
						stat.Update()

						// Verify AuthInfo
						cAuthInfo, ok := authInfo.(*AuthInfo)
						if !ok || cAuthInfo.Side != ServerSide ||
							cAuthInfo.RemoteIdentity != serverAddr {
							errChan <- fmt.Errorf("invalid server auth info")
							return
						}
						conn.Close()
					}
					errChan <- err
				}(accountClient)

				// server -> client handshake
				go func(accountName string) {
					defer func() {
						if r := recover(); r != nil {
							errChan <- fmt.Errorf("server panic: %v", r)
						}
					}()

					ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
					defer cancel()

					conn, authInfo, err := serverHS.ServerHandshake(ctx)
					if err == nil {
						// Verify AuthInfo
						sAuthInfo, ok := authInfo.(*AuthInfo)
						if !ok || sAuthInfo.Side != ClientSide ||
							sAuthInfo.RemoteIdentity != clientAddr {
							errChan <- fmt.Errorf("invalid client auth info")
							return
						}
						conn.Close()
					}
					errChan <- err
				}(accountServer)
			}

			expectedCalls := tc.numHandshakes * 2
			if tc.newConnWait && tc.numHandshakes > maxConcurrentHandshakes {
				// When we exceed the limit, we expect exactly (tc.numHandshakes*2 - maxConcurrentHandshakes*2) failures
				// Because both client and server handshakes are limited
				expectedCalls = maxConcurrentHandshakes * 2
			}
			for i := 0; i < expectedCalls; i++ {
				select {
				case <-newConnCalledCh:
					// Received a signal from a handshake that actually called NewConn
				case <-time.After(defaultTestTimeout):
					t.Fatal("timeout waiting for NewConn to be called")
				}
			}

			if tc.newConnWait {
				// Let NewConn to proceed
				cond.Broadcast()
			}

			// Collect results
			var successes, failures int
			var failureReasons []string
			for i := 0; i < tc.numHandshakes*2; i++ {
				err := <-errChan
				if err == nil {
					successes++
				} else {
					failures++
					failureReasons = append(failureReasons, fmt.Sprintf("handshake %d failed: %v", i, err))
				}
			}
			t.Logf("Successes: %d, Failures: %d", successes, failures)

			// Verify results
			if tc.expectSuccess {
				require.Equal(t, tc.numHandshakes*2, successes, "expected all handshakes to succeed")
				require.Equal(t, 0, failures, "expected no failures")
			} else {
				// Log failure reasons if any
				if len(failureReasons) > 0 {
					t.Logf("Handshake failures:\n%s", strings.Join(failureReasons, "\n"))
				}
				// When we exceed the limit, we expect exactly (tc.numHandshakes*2 - maxConcurrentHandshakes*2) failures
				// Because both client and server handshakes are limited
				expectedFailures := tc.numHandshakes*2 - maxConcurrentHandshakes*2
				require.Equal(t, expectedFailures, failures,
					"expected %d failures due to exceeding concurrent handshake limit", expectedFailures)

				// And maxConcurrentHandshakes*2 successes (the ones that got through before limit was hit)
				require.Equal(t, maxConcurrentHandshakes*2, successes,
					"expected %d successful handshakes", maxConcurrentHandshakes*2)
			}

			// Verify concurrent handshake limits
			require.LessOrEqual(t, stat.MaxConcurrentCalls, maxConcurrentHandshakes,
				"concurrent handshakes exceeded limit")
		})
	}
}

func TestHandshakerContext(t *testing.T) {
	kr := CreateTestKeyring()

	addresses := SetupTestAccounts(t, kr, []string{"client", "server"})
	clientAddr := addresses[0]
	serverAddr := addresses[1]

	clientKE := SetupTestKeyExchange(t, kr, clientAddr, securekeyx.Simplenode)

	t.Run("Context timeout", func(t *testing.T) {
		client, server := net.Pipe()
		defer server.Close()

		// Create a conn that will hang on reads
		conn := testutil.NewTestConnWithReadLatency(client, time.Hour) // Much longer than the timeout

		// Server side goroutine to prevent blocking on writes
		go func() {
			buf := make([]byte, 1024)
			server.Read(buf)
		}()

		hs := NewClientHandshaker(clientKE, conn, serverAddr, nil)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, _, err := hs.ClientHandshake(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("Context cancellation", func(t *testing.T) {
		client, server := net.Pipe()
		defer server.Close()

		// Create a conn that will hang on reads
		conn := testutil.NewTestConnWithReadLatency(client, time.Hour) // Much longer than before cancellation

		// Server side goroutine to prevent blocking on writes
		go func() {
			buf := make([]byte, 1024)
			server.Read(buf)
		}()

		hs := NewClientHandshaker(clientKE, conn, serverAddr, nil)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()

		_, _, err := hs.ClientHandshake(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestUnresponsivePeer(t *testing.T) {
	kr := CreateTestKeyring()

	addresses := SetupTestAccounts(t, kr, []string{"client", "server"})
	clientAddr := addresses[0]
	serverAddr := addresses[1]

	clientKE := SetupTestKeyExchange(t, kr, clientAddr, securekeyx.Simplenode)

	handshakeTimeout := 100 * time.Millisecond
	conn := testutil.NewUnresponsiveTestConn(time.Hour) // Create unresponsive conn

	// Create handshaker with short timeout for testing
	hs := newTestHandshaker(clientKE, conn, serverAddr, ClientSide, handshakeTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout*2)
	defer cancel()

	t.Logf("Starting handshake with timeout %v", handshakeTimeout)
	_, _, err := hs.ClientHandshake(ctx)
	t.Logf("Handshake completed with error: %v", err)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPeerNotResponding)
}

// TestClient_InvalidSide tests the case where the client handshake is attempted with an
// invalid side
func TestClient_InvalidSide(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSide)
}

// TestServer_InvalidSide tests the case where the server handshake is attempted with an
// invalid side
func TestServer_InvalidSide(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSide)
}

// TestClient_ReadResponse tests the case where the client handshake fails
func TestClient_ReadResponse(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.overrideReadResponseWithTimeout(func(ctx context.Context, lastWrite time.Time) ([]byte, []byte, error) {
		return nil, nil, errors.New("failed to read handshake response")
	})

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read handshake response")
}

// TestClient_ParseInvalidHandshakeMessage tests the case where the client handshake fails
func TestClient_ParseInvalidHandshakeMessage(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.overrideParseHandshakeMessage(func([]byte) (*lumeraidtypes.HandshakeInfo, error) {
		return nil, errors.New("failed to parse handshake message")
	})
	ti.overrideReadResponseWithTimeoutSuccess()

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse handshake message")
}

func TestServer_ReadRequest(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeout(func(ctx context.Context) ([]byte, []byte, error) {
		return nil, nil, errors.New("failed to read handshake request")
	})
	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read handshake request")
}

func TestServer_ParseInvalidHandshakeMessage(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeoutSuccess()
	ti.overrideParseHandshakeMessage(func([]byte) (*lumeraidtypes.HandshakeInfo, error) {
		return nil, errors.New("failed to parse handshake message")
	})

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse handshake message")
}

func TestClient_CreateRequestFailure(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.ke.EXPECT().CreateRequest(ti.remoteAddr).Return(nil, nil, errors.New("failed to create handshake request: mock error"))

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create handshake request")
}

func TestClient_ComputeSharedSecretFailure(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.overrideReadResponseWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.mockComputeSharedSecret(func([]byte, []byte) ([]byte, error) {
		return nil, errors.New("failed to compute shared secret")
	})

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to compute shared secret")
}

func TestClientHandshakeSemaphore(t *testing.T) {
	kr := CreateTestKeyring()

	addresses := SetupTestAccounts(t, kr, []string{"client", "server"})
	clientAddr := addresses[0]
	serverAddr := addresses[1]

	clientKE := SetupTestKeyExchange(t, kr, clientAddr, securekeyx.Simplenode)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// First handshake takes all semaphore slots
	for i := 0; i < maxConcurrentHandshakes; i++ {
		_, _ = net.Pipe() // create pipe just to prevent blocking
		hs := NewClientHandshaker(clientKE, client, serverAddr, nil)
		go func() {
			ctx := context.Background()
			_, _, _ = hs.ClientHandshake(ctx)
		}()
	}

	// Now try another handshake with timeout=0, it should fail to acquire semaphore
	hs := NewClientHandshaker(clientKE, client, serverAddr, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, _, err := hs.ClientHandshake(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestServerHandshakeSemaphore(t *testing.T) {
	kr := CreateTestKeyring()

	addresses := SetupTestAccounts(t, kr, []string{"client", "server"})
	serverAddr := addresses[1]

	serverKE := SetupTestKeyExchange(t, kr, serverAddr, securekeyx.Simplenode)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// First handshake takes all semaphore slots
	for i := 0; i < maxConcurrentHandshakes; i++ {
		_, _ = net.Pipe() // create pipe just to prevent blocking
		hs := NewServerHandshaker(serverKE, server, nil)
		go func() {
			ctx := context.Background()
			_, _, _ = hs.ServerHandshake(ctx)
		}()
	}

	// Now try another handshake with timeout=0, it should fail to acquire semaphore
	hs := NewServerHandshaker(serverKE, server, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, _, err := hs.ServerHandshake(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDefaultReadRequestWithTimeout_ContextDone(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel context

	ti.overrideReadRequestWithTimeoutSuccess()

	bytes, sig, err := ti.hs.defaultReadRequestWithTimeout(ctx)
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Equal(t, context.Canceled, err)
}

func TestDefaultReadRequestWithTimeout_FailedReceive(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReceiveHandshakeMessage(func(conn net.Conn) ([]byte, []byte, error) {
		return nil, nil, errors.New("mocked receive error")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bytes, sig, err := ti.hs.defaultReadRequestWithTimeout(ctx)
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mocked receive error")
}

func TestDefaultReadRequestWithTimeout_EmptyResult(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReceiveHandshakeMessage(func(conn net.Conn) ([]byte, []byte, error) {
		return []byte(""), nil, nil
	})

	bytes, sig, err := ti.hs.defaultReadRequestWithTimeout(context.Background())
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), ErrPeerNotResponding.Error())
}

func TestDefaultReadResponseWithTimeout_ContextDone(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.setHandshakeTimeout(time.Minute)
	ti.overrideReceiveHandshakeMessageSuccess()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel context

	bytes, sig, err := ti.hs.defaultReadResponseWithTimeout(ctx, time.Now())
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Equal(t, context.Canceled, err)
}

func TestDefaultReadResponseWithTimeout_FailedReceive(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.setHandshakeTimeout(time.Minute)
	ti.overrideReceiveHandshakeMessage(func(conn net.Conn) ([]byte, []byte, error) {
		return nil, nil, errors.New("mocked receive error")
	})

	bytes, sig, err := ti.hs.defaultReadResponseWithTimeout(context.Background(), time.Now())
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to receive handshake response")
}

func TestDefaultReadResponsetWithTimeout_EmptyResult(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.setHandshakeTimeout(100*time.Millisecond)
	ti.overrideReceiveHandshakeMessage(func(conn net.Conn) ([]byte, []byte, error) {
		time.Sleep(200 * time.Millisecond) // Simulate slow response
		return []byte(""), nil, nil
	})

	bytes, sig, err := ti.hs.defaultReadResponseWithTimeout(context.Background(), time.Now())
	require.Nil(t, bytes)
	require.Nil(t, sig)
	require.Error(t, err)
	require.Contains(t, err.Error(), ErrPeerNotResponding.Error())
}

func TestNewHandshaker_ClientOptionsProvided(t *testing.T) {
	t.Parallel()
	opts := &ClientHandshakerOptions{}
	ti := newHSClientInterceptor(t, opts)
	defer ti.cleanup()

	require.Equal(t, opts, ti.hs.clientOpts)
}

func TestNewHandshaker_ClientOptionsDefault(t *testing.T) {
	t.Parallel()
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	require.NotNil(t, ti.hs.clientOpts)
	require.Equal(t, DefaultClientHandshakerOptions(), ti.hs.clientOpts)
}

func TestNewHandshaker_ServerOptionsProvided(t *testing.T) {
	t.Parallel()
	opts := &ServerHandshakerOptions{}
	ti := newHSServerInterceptor(t, opts)
	defer ti.cleanup()

	require.Equal(t, opts, ti.hs.serverOpts)
}

func TestNewHandshaker_ServerOptionsDefault(t *testing.T) {
	t.Parallel()
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	require.NotNil(t, ti.hs.serverOpts)
	require.Equal(t, DefaultServerHandshakerOptions(), ti.hs.serverOpts)
}

func TestNewHandshaker_ClientOptionsInvalidType(t *testing.T) {
	t.Parallel()
	ti := newHSClientInterceptor(t, "invalid_type")
	defer ti.cleanup()

	require.NotNil(t, ti.hs.clientOpts)
	require.Equal(t, DefaultClientHandshakerOptions(), ti.hs.clientOpts)
}

func TestNewHandshaker_ServerOptionsInvalidType(t *testing.T) {
	t.Parallel()
	ti := newHSServerInterceptor(t, "invalid_type")
	defer ti.cleanup()

	require.NotNil(t, ti.hs.serverOpts)
	require.Equal(t, DefaultServerHandshakerOptions(), ti.hs.serverOpts)
}

func TestClientHandshake_RemoteAddressMismatch(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.mockCreateRequestSuccess()
	ti.overrideReadResponseWithTimeoutSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.overrideParseHandshakeMessage(func([]byte) (*lumeraidtypes.HandshakeInfo, error) {
		return &lumeraidtypes.HandshakeInfo{Address: "different_remote"}, nil
	})

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("remote address mismatch: different_remote != %s", testRemoteAddr))
}

func TestServerHandshake_ExpandKeyFailure(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.mockComputeSharedSecretSuccess()
	ti.mockLocalAddress()
	ti.overrideExpandKey(func([]byte, string, []byte) ([]byte, error) {
		return nil, errors.New("mocked expand key error")
	})

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "mocked expand key error")
}

func TestClientHandshake_ExpandKeyFailure(t *testing.T) {
	ti := newHSClientInterceptor(t, nil)
	defer ti.cleanup()

	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.overrideReadResponseWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.mockComputeSharedSecretSuccess()
	ti.mockLocalAddress()
	ti.overrideExpandKey(func([]byte, string, []byte) ([]byte, error) {
		return nil, errors.New("mocked expand key error")
	})

	_, _, err := ti.hs.ClientHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "mocked expand key error")
}

func TestServerHandshake_CreateRequestFailure(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.ke.EXPECT().CreateRequest(ti.remoteAddr).Return(nil, nil, errors.New("failed to create handshake request"))

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create handshake request")
}

func TestServerHandshake_SendHandshakeFailure(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessage(func(conn net.Conn, handshakeBytes, signature []byte) error {
		return errors.New("failed to send handshake message")
	})

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to send handshake message")
}

func TestServerHandshake_ComputeSharedSecretFailure(t *testing.T) {
	ti := newHSServerInterceptor(t, nil)
	defer ti.cleanup()

	ti.overrideReadRequestWithTimeoutSuccess()
	ti.overrideParseHandshakeMessageSuccess(testRemoteAddr, securekeyx.Supernode)
	ti.mockCreateRequestSuccess()
	ti.overrideSendHandshakeMessageSuccess()
	ti.mockComputeSharedSecret(func([]byte, []byte) ([]byte, error) {
		return nil, errors.New("failed to compute shared secret")
	})

	_, _, err := ti.hs.ServerHandshake(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to compute shared secret")
}

