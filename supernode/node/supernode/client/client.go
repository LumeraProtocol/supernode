package client

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/random"
	node "github.com/LumeraProtocol/supernode/supernode/node/supernode"

	"google.golang.org/grpc"
)

// this implements SN's GRPC methods that call another SN during Cascade Registration
// meaning - these methods implements client side of SN to SN GRPC communication

type client struct {
	//secClient alts.SecClient
	//secInfo   *alts.SecInfo
}

// Connect implements node.Client.Connect()
func (client *client) Connect(ctx context.Context, address string) (node.ConnectionInterface, error) {
	//grpclog.SetLoggerV2(log.NewLoggerWithErrorLevel())
	//
	id, _ := random.String(8, random.Base62Chars)
	//ctx = log.ContextWithPrefix(ctx, fmt.Sprintf("%s-%s", logPrefix, id))
	//
	//// Define the keep-alive parameters
	//ka := keepalive.ClientParameters{
	//	Time:                30 * time.Minute,
	//	Timeout:             30 * time.Minute,
	//	PermitWithoutStream: true,
	//}
	//
	//if client.secClient == nil || client.secInfo == nil {
	//	return nil, errors.Errorf("secClient or secInfo don't initialize")
	//}
	//
	//altsTCClient := credentials.NewClientCreds(client.secClient, client.secInfo)
	var grpcConn *grpc.ClientConn
	//var err error
	//if os.Getenv("INTEGRATION_TEST_ENV") == "true" {
	//	grpcConn, err = grpc.DialContext(ctx, address,
	//		//lint:ignore SA1019 we want to ignore this for now
	//		grpc.WithInsecure(),
	//		grpc.WithBlock(),
	//		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100000000), grpc.MaxCallSendMsgSize(100000000)),
	//		grpc.WithKeepaliveParams(ka),
	//	)
	//} else {
	//	grpcConn, err = grpc.DialContext(ctx, address,
	//		grpc.WithTransportCredentials(altsTCClient),
	//		grpc.WithBlock(),
	//		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100000000), grpc.MaxCallSendMsgSize(100000000)),
	//		grpc.WithKeepaliveParams(ka),
	//	)
	//}
	//
	//if err != nil {
	//	log.WithContext(ctx).WithError(err).Error("DialContext err")
	//	return nil, errors.Errorf("dial address %s: %w", address, err)
	//}
	//
	//log.WithContext(ctx).Debugf("Connected to %s", address)
	//
	conn := newClientConn(id, grpcConn)
	go func() {
		log.WithContext(ctx).Debugf("Disconnected %s", grpcConn.Target())
	}()

	return conn, nil
}
