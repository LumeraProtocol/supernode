package handshake

import (
	"context"
	"net"

	"google.golang.org/grpc/credentials"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	. "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials/alts/common"
)

// AuthInfo holds the result of ALTS handshaking
type AuthInfo struct {
	// CommonAuthInfo contains authenticated information common to AuthInfo implementations.
	// It should be embedded in a struct implementing AuthInfo to provide additional information
	// about the credentials.
	credentials.CommonAuthInfo

	// Side indicates if this is the client or server side
	Side Side
	// RemotePeerType indicates if this is a supernode or simplenode
	RemotePeerType securekeyx.PeerType
	// RemoteIdentity is the remote Cosmos address
	RemoteIdentity string
}

// AuthType returns the name of the authentication mechanism
func (a *AuthInfo) AuthType() string {
	return LumeraALTSProtocol
}

// NewAuthInfo creates a new AuthInfo object
func NewAuthInfo(side Side, peerType securekeyx.PeerType, remoteIdentity string) credentials.AuthInfo {
	return &AuthInfo{
		Side:           side,
		RemotePeerType: peerType,
		RemoteIdentity: remoteIdentity,
	}
}

// Handshaker defines the ALTS handshaker interface.
type Handshaker interface {
	// ClientHandshake starts and completes a client-side handshaking and
	// returns a secure connection and corresponding auth information.
	ClientHandshake(ctx context.Context) (net.Conn, credentials.AuthInfo, error)
	// ServerHandshake starts and completes a server-side handshaking and
	// returns a secure connection and corresponding auth information.
	ServerHandshake(ctx context.Context) (net.Conn, credentials.AuthInfo, error)
	// Close terminates the Handshaker.
	Close()
}
