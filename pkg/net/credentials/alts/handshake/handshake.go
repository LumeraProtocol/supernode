package handshake

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/credentials"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/conn"
)

const (
	defaultTimeout          = 30 * time.Second
	maxConcurrentHandshakes = 100
)

var (
	// Control number of concurrent handshakes
	clientHandshakes = semaphore.NewWeighted(maxConcurrentHandshakes)
	serverHandshakes = semaphore.NewWeighted(maxConcurrentHandshakes)

	// ErrPeerNotResponding occurs when peer doesn't respond in time
	ErrPeerNotResponding = errors.New("peer is not responding")
	// ErrInvalidSide occurs when handshaker is used with wrong side
	ErrInvalidSide = errors.New("handshaker invalid side")
)

// ClientHandshakerOptions contains the client handshaker options that can
// provided by the caller.
type ClientHandshakerOptions struct {
}

// ServerHandshakerOptions contains the server handshaker options that can
// provided by the caller.
type ServerHandshakerOptions struct {
}

// DefaultClientHandshakerOptions returns the default client handshaker options.
func DefaultClientHandshakerOptions() *ClientHandshakerOptions {
	return &ClientHandshakerOptions{}
}

// DefaultServerHandshakerOptions returns the default client handshaker options.
func DefaultServerHandshakerOptions() *ServerHandshakerOptions {
	return &ServerHandshakerOptions{}
}

type ReadResponseWithTimeoutFunc func(ctx context.Context, lastWrite time.Time) ([]byte, []byte, error)
type ReadRequestWithTimeoutFunc func(ctx context.Context) ([]byte, []byte, error)

// secureHandshaker implements the ALTS handshaker interface
type secureHandshaker struct {
	// Network connection to the peer.
	conn net.Conn
	// Key exchange interface.
	keyExchanger securekeyx.KeyExchanger
	// Remote address for client mode.
	remoteAddr string
	// Side doing handshake (client or server).
	side Side
	// client handshake options.
	clientOpts *ClientHandshakerOptions
	// server handshake options.
	serverOpts *ServerHandshakerOptions
	// Selected record protocol.
	protocol string
	// Handshake timeout.
	timeout time.Duration

	readResponseWithTimeoutFn ReadResponseWithTimeoutFunc
	readRequestWithTimeoutFn  ReadRequestWithTimeoutFunc
}

type handshakeData struct {
	bytes     []byte
	signature []byte
	err       error
}

// newHandshaker creates a handshaker with custom timeout
func newHandshaker(keyExchange securekeyx.KeyExchanger, conn net.Conn, remoteAddr string,
	side Side, timeout time.Duration, opts interface{}) *secureHandshaker {

	hs := &secureHandshaker{
		conn:        conn,
		keyExchanger: keyExchange,
		remoteAddr:  remoteAddr,
		side:        side,
		protocol:    RecordProtocolXChaCha20Poly1305ReKey, // Default to XChaCha20-Poly1305
		timeout:     timeout,
	}

	if side == ClientSide {
		// If opts is provided, attempt to use it as client options.
		if opts != nil {
			if clientOpts, ok := opts.(*ClientHandshakerOptions); ok {
				hs.clientOpts = clientOpts
			} else {
				// Handle the case where the provided options are not of the expected type.
				// Here we simply default, but you could also return an error.
				hs.clientOpts = DefaultClientHandshakerOptions()
			}
		} else {
			hs.clientOpts = DefaultClientHandshakerOptions()
		}
	} else if side == ServerSide {
		// If opts is provided, attempt to use it as server options.
		if opts != nil {
			if serverOpts, ok := opts.(*ServerHandshakerOptions); ok {
				hs.serverOpts = serverOpts
			} else {
				hs.serverOpts = DefaultServerHandshakerOptions()
			}
		} else {
			hs.serverOpts = DefaultServerHandshakerOptions()
		}
	}

	return hs
}

// NewClientHandshaker creates a client-side handshaker
func NewClientHandshaker(keyExchange securekeyx.KeyExchanger, conn net.Conn, remoteAddr string, opts *ClientHandshakerOptions) *secureHandshaker {
	return newHandshaker(keyExchange, conn, remoteAddr, ClientSide, defaultTimeout, opts)
}

// NewServerHandshaker creates a server-side handshaker
func NewServerHandshaker(keyExchange securekeyx.KeyExchanger, conn net.Conn, opts *ServerHandshakerOptions) *secureHandshaker {
	return newHandshaker(keyExchange, conn, "", ServerSide, defaultTimeout, opts)
}

// For testing purposes
func newTestHandshaker(keyExchange securekeyx.KeyExchanger, conn net.Conn, remoteAddr string,
	side Side, timeout time.Duration) *secureHandshaker {
	return newHandshaker(keyExchange, conn, remoteAddr, side, timeout, nil)
}

// ClientHandshake performs client-side handshake
func (h *secureHandshaker) ClientHandshake(ctx context.Context) (net.Conn, credentials.AuthInfo, error) {
	if err := clientHandshakes.Acquire(ctx, 1); err != nil {
		return nil, nil, err
	}
	defer func() {
		clientHandshakes.Release(1)
	}()

	if h.side != ClientSide {
		return nil, nil, ErrInvalidSide
	}

	// Set timeout if not set in context
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	var lastWriteTime time.Time

	// Create handshake request
	handshakeBytes, signature, err := h.keyExchanger.CreateRequest(h.remoteAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create handshake request: %w", err)
	}

	// Send handshake request
	lastWriteTime = time.Now()
	if err := SendHandshakeMessage(h.conn, handshakeBytes, signature); err != nil {
		return nil, nil, fmt.Errorf("failed to send handshake request: %w", err)
	}

	// Read response with timeout check
	respHandshakeBytes, respSig, err := h.readResponseWithTimeout(ctx, lastWriteTime)
	if err != nil {
		return nil, nil, err
	}

	remoteInfo, err := ParseHandshakeMessage(respHandshakeBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse handshake response: %w", err)
	}

	if remoteInfo.Address != h.remoteAddr {
		return nil, nil, fmt.Errorf("remote address mismatch: %s != %s", remoteInfo.Address, h.remoteAddr)
	}

	// Compute shared secret
	sharedSecret, err := h.keyExchanger.ComputeSharedSecret(respHandshakeBytes, respSig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute shared secret: %w", err)
	}

	// Create info for HKDF that binds the key to this specific handshake
	// info format: [protocol]-[client address]-[server address]
	info := []byte(fmt.Sprintf("%s-%s-%s", h.protocol, h.keyExchanger.LocalAddress(), h.remoteAddr))

	// Expand the shared secret for the specific protocol
	expandedKey, err := ExpandKey(sharedSecret, h.protocol, info)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to expand key: %w", err)
	}
	// Clear shared secret
	sharedSecret = nil
	h.Close()

	// Create ALTS connection
	altsConn, err := NewConn(h.conn, h.side, h.protocol, expandedKey, nil)
	if err != nil {
		fmt.Println("Failed to create ALTS connection on client: %w", err)
		return nil, nil, fmt.Errorf("failed to create ALTS connection: %w", err)
	}

	// Clear expanded key
	expandedKey = nil

	// Create AuthInfo
	serverAuthInfo := &AuthInfo{
		CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity},

		Side:           ServerSide,
		RemotePeerType: securekeyx.PeerType(remoteInfo.PeerType),
		RemoteIdentity: remoteInfo.Address,
	}

	return altsConn, serverAuthInfo, nil
}

// ServerHandshake performs server-side handshake
func (h *secureHandshaker) ServerHandshake(ctx context.Context) (net.Conn, credentials.AuthInfo, error) {
	// Acquire semaphore
	if err := serverHandshakes.Acquire(ctx, 1); err != nil {
		return nil, nil, err
	}
	defer func() {
		serverHandshakes.Release(1)
	}()

	if h.side != ServerSide {
		return nil, nil, ErrInvalidSide
	}

	// Set timeout if not set in context
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	// Read initial request with timeout check
	reqBytes, reqSig, err := h.readRequestWithTimeout(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Parse remote handshake message
	clientInfo, err := ParseHandshakeMessage(reqBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse remote address: %w", err)
	}

	// Create handshake response
	respBytes, respSig, err := h.keyExchanger.CreateRequest(clientInfo.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create handshake response: %w", err)
	}

	// Send response
	if err := SendHandshakeMessage(h.conn, respBytes, respSig); err != nil {
		return nil, nil, fmt.Errorf("failed to send handshake response: %w", err)
	}

	// Compute shared secret
	sharedSecret, err := h.keyExchanger.ComputeSharedSecret(reqBytes, reqSig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compute shared secret: %w", err)
	}

	// Create info for HKDF that binds the key to this specific handshake
	// info format: [protocol]-[client address]-[server address]
	info := []byte(fmt.Sprintf("%s-%s-%s", h.protocol, clientInfo.Address, h.keyExchanger.LocalAddress()))

	// Expand the shared secret for the specific protocol
	expandedKey, err := ExpandKey(sharedSecret, h.protocol, info)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to expand key: %w", err)
	}
	// Clear shared secret
	sharedSecret = nil

	// Create ALTS connection
	altsConn, err := NewConn(h.conn, h.side, h.protocol, expandedKey, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ALTS connection: %w", err)
	}

	// Clear expanded key
	expandedKey = nil
	h.Close()

	// Create AuthInfo
	clientAuthInfo := &AuthInfo{
		CommonAuthInfo: credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity},

		Side:           ClientSide,
		RemotePeerType: securekeyx.PeerType(clientInfo.PeerType),
		RemoteIdentity: clientInfo.Address,
	}

	return altsConn, clientAuthInfo, nil
}

func (h *secureHandshaker) defaultReadRequestWithTimeout(ctx context.Context) ([]byte, []byte, error) {
	readChan := make(chan handshakeData, 1)

	go func() {
		reqBytes, reqSig, err := ReceiveHandshakeMessage(h.conn)
		readChan <- handshakeData{reqBytes, reqSig, err}
	}()

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case result := <-readChan:
		if result.err != nil {
			return nil, nil, fmt.Errorf("failed to receive handshake request: %w", result.err)
		}
		if len(result.bytes) == 0 {
			return nil, nil, ErrPeerNotResponding
		}
		return result.bytes, result.signature, nil
	}
}

func (h *secureHandshaker) defaultReadResponseWithTimeout(ctx context.Context, lastWrite time.Time) ([]byte, []byte, error) {
	readChan := make(chan handshakeData, 1)
	readStartTime := time.Now()

	go func() {
		respBytes, respSig, err := ReceiveHandshakeMessage(h.conn)
		readChan <- handshakeData{respBytes, respSig, err}
	}()

	select {
	case <-ctx.Done():
		// If we've been reading for long enough and got no response, treat as unresponsive
		if time.Since(readStartTime) >= h.timeout {
			return nil, nil, ErrPeerNotResponding
		}
		return nil, nil, ctx.Err()
	case result := <-readChan:
		if result.err != nil {
			return nil, nil, fmt.Errorf("failed to receive handshake response: %w", result.err)
		}
		// If nothing was written and nothing was read, peer is not responding
		if len(result.bytes) == 0 && time.Since(lastWrite) > h.timeout {
			return nil, nil, ErrPeerNotResponding
		}
		return result.bytes, result.signature, nil
	}
}

func (h *secureHandshaker) readResponseWithTimeout(ctx context.Context, lastWrite time.Time) ([]byte, []byte, error) {
	if h.readResponseWithTimeoutFn != nil {
		return h.readResponseWithTimeoutFn(ctx, lastWrite)
	}
	// Default behavior
	return h.defaultReadResponseWithTimeout(ctx, lastWrite)
}

func (h *secureHandshaker) readRequestWithTimeout(ctx context.Context) ([]byte, []byte, error) {
	if h.readRequestWithTimeoutFn != nil {
		return h.readRequestWithTimeoutFn(ctx)
	}
	// Default behavior
	return h.defaultReadRequestWithTimeout(ctx)
}

// Close closes the handshaker
func (h *secureHandshaker) Close() {
	// Nothing to close in our implementation
}
