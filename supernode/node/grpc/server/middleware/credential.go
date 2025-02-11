package middleware

import (
	"github.com/LumeraProtocol/supernode/common/net/credentials"
	"github.com/LumeraProtocol/supernode/common/net/credentials/alts"
	"google.golang.org/grpc"
)

// AltsCredential returns a ServerOption that sets the Creds for the server.
func AltsCredential(secClient alts.SecClient, secInfo *alts.SecInfo) grpc.ServerOption {
	altsTCServer := credentials.NewServerCreds(secClient, secInfo)
	return grpc.Creds(altsTCServer)
}
