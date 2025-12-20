package reachability

import (
	"context"
	"net"

	"google.golang.org/grpc/peer"

	althandshake "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials/alts/handshake"
)

// GrpcRemoteIdentityAndAddr extracts the remote identity (if using Lumera ALTS)
// and the remote network address from an inbound gRPC context.
func GrpcRemoteIdentityAndAddr(ctx context.Context) (identity string, addr net.Addr) {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return "", nil
	}
	if p.AuthInfo != nil {
		if ai, ok := p.AuthInfo.(*althandshake.AuthInfo); ok {
			identity = ai.RemoteIdentity
		}
	}
	return identity, p.Addr
}
