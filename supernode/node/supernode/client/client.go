package client

import (
	pb "github.com/LumeraProtocol/supernode/gen/supernode/supernode"
	"github.com/LumeraProtocol/supernode/supernode/node"

	"google.golang.org/grpc"
)

// this implements SN's GRPC methods that call another SN during Cascade Registration
// meaning - these methods implements client side of SN to SN GRPC communication

type RegisterCascade struct {
	conn   *grpc.ClientConn
	client pb.CascadeServiceClient
	sessID string
}

func NewRegisterCascade(conn *grpc.ClientConn) node.RegisterCascadeInterface {
	return &RegisterCascade{
		conn:   conn,
		client: pb.NewCascadeServiceClient(conn),
	}
}
