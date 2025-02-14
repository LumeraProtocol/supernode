package credentials

import (
	"fmt"
	"context"
	"net"
	"sync"
	"crypto/ecdh"

	"google.golang.org/grpc/credentials"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"

	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/handshake"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
)

const lumeraALTSProtocol = "lumera-alts"

var (
	keyExchangers = map[string]*securekeyx.SecureKeyExchange{}
	keyExMutex    sync.Mutex
)

// CommonOptions contains the shared configuration for both client and server
type CommonOptions struct {
	Keyring    		keyring.Keyring
	LocalIdentity   string  // Local Cosmos address
	PeerType   		securekeyx.PeerType // Local peer type
	Curve      		ecdh.Curve
}

// ClientOptions contains client-specific configuration
type ClientOptions struct {
	CommonOptions
	RemoteIdentity string // Remote Cosmos address
}

// ServerOptions contains server-specific configuration
type ServerOptions struct {
	CommonOptions
}

// DefaultClientOptions creates a new ClientOptions object with the default
// values.
func DefaultClientOptions() *ClientOptions {
	return &ClientOptions{
		CommonOptions: CommonOptions{
			PeerType: securekeyx.Simplenode,
			Curve:    ecdh.P256(),
		},
	}
}

// DefaultServerOptions creates a new ServerOptions object with the default
// values.
func DefaultServerOptions() *ServerOptions {
	return &ServerOptions{
		CommonOptions: CommonOptions{
			PeerType: securekeyx.Supernode,
			Curve:    ecdh.P256(),
		},
	}
}

// LumeraTC implements the TransportCredentials interface for Lumera secure communication
type LumeraTC struct {
	info            *credentials.ProtocolInfo
	side         	Side
	remoteIdentity  string	
	keyExchanger    *securekeyx.SecureKeyExchange
}

// NewTransportCredentials creates a new TransportCredentials with the given options
func NewTransportCredentials(side Side, opts interface{}) (credentials.TransportCredentials, error) {
	if opts == nil {
		return nil, fmt.Errorf("credentials should be provided")
	}
	var optsCommon *CommonOptions
	var remoteIdentity string

	if commonOpts, ok := opts.(*CommonOptions); ok {
		optsCommon = commonOpts
	} else {
		return nil, fmt.Errorf("invalid credentials type")
	}
	if side == ClientSide {
		if optsClient, ok := opts.(*ClientOptions); ok {
			remoteIdentity = optsClient.RemoteIdentity
		}
	}

	if optsCommon.Curve == nil {
		optsCommon.Curve = ecdh.P256() // Default to P-256 if not specified
	}

	keyExMutex.Lock()
	keyExchanger, exists := keyExchangers[optsCommon.LocalIdentity]
	if !exists {
		keyExchanger, err := securekeyx.NewSecureKeyExchange(
			optsCommon.Keyring,
			optsCommon.LocalIdentity,
			optsCommon.PeerType,
			optsCommon.Curve,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create secure key exchange: %w", err)
		}
		keyExchangers[optsCommon.LocalIdentity] = keyExchanger
	}
	keyExMutex.Unlock()

	return &LumeraTC{
		info: &credentials.ProtocolInfo{
			SecurityProtocol: lumeraALTSProtocol,
			SecurityVersion: "1.0",
		},
		side: side,
		remoteIdentity: remoteIdentity,
		keyExchanger: keyExchanger,
	}, nil
}

// NewClientCreds creates a TransportCredentials for the client side
func NewClientCreds(opts *ClientOptions) (credentials.TransportCredentials, error) {
	return NewTransportCredentials(ClientSide, &opts.CommonOptions)
}

// NewServerCreds creates a TransportCredentials for the server side
func NewServerCreds(opts *ServerOptions) (credentials.TransportCredentials, error) {
	return NewTransportCredentials(ServerSide, &opts.CommonOptions)
}

// ClientHandshake performs the client-side handshake
func (l *LumeraTC) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	//ctx = log.ContextWithPrefix(ctx, "lumera-handshake")
	opts := handshake.DefaultClientHandshakerOptions()
	clientHS := handshake.NewClientHandshaker(l.keyExchanger, rawConn, l.remoteIdentity, opts)

	secureConn, serverAuthInfo, err := clientHS.ClientHandshake(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to complete client handshake: %w", err)
	}

	return secureConn, serverAuthInfo, nil
}

// ServerHandshake performs the server-side handshake
func (l *LumeraTC) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	// ctx = log.ContextWithPrefix(ctx, "alts-handshake")
	opts := handshake.DefaultServerHandshakerOptions()
	serverHS := handshake.NewServerHandshaker(l.keyExchanger, rawConn, opts)

	secureConn, clientAuthInfo, err := serverHS.ServerHandshake(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to complete server handshake: %w", err)
	}

	return secureConn, clientAuthInfo, nil
}

func (l *LumeraTC) Info() credentials.ProtocolInfo {
	return *l.info
}

func (l *LumeraTC) Clone() credentials.TransportCredentials {
	return &LumeraTC{
		info: l.info,
		side: l.side,
		remoteIdentity: l.remoteIdentity,
		keyExchanger: l.keyExchanger,
	}
}

func (l *LumeraTC) OverrideServerName(serverNameOverride string) error {
	l.info.ServerName = serverNameOverride
	return nil
}

// LumeraAuthInfo implements the AuthInfo interface
type LumeraAuthInfo struct {
	credentials.CommonAuthInfo
}

func (l *LumeraAuthInfo) AuthType() string {
	return lumeraALTSProtocol
}