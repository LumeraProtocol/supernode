package client

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	netgrpcclient "github.com/LumeraProtocol/supernode/pkg/net/grpc/client"
	"github.com/LumeraProtocol/supernode/pkg/random"
	node "github.com/LumeraProtocol/supernode/supernode/node/supernode"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	_ "google.golang.org/grpc/keepalive"
)

// this implements SN's GRPC methods that call another SN during Cascade Registration
// meaning - these methods implements client side of SN to SN GRPC communication

type Client struct {
	*netgrpcclient.Client
	KeyRing             keyring.Keyring
	SuperNodeAccAddress string
}

// Connect implements node.Client.Connect()
func (c *Client) Connect(ctx context.Context, address string) (node.ConnectionInterface, error) {
	clientOptions := netgrpcclient.DefaultClientOptions()
	clientOptions.ConnWaitTime = 30 * time.Minute
	clientOptions.MinConnectTimeout = 30 * time.Minute
	clientOptions.EnableRetries = false

	id, _ := random.String(8, random.Base62Chars)

	grpcConn, err := c.Client.Connect(ctx, address, clientOptions)
	if err != nil {
		log.WithContext(ctx).WithError(err).Error("DialContext err")
		return nil, errors.Errorf("dial address %s: %w", address, err)
	}

	log.WithContext(ctx).Debugf("Connected to %s", address)

	conn := newClientConn(id, grpcConn)

	go func() {
		//<-conn.Done()
		log.WithContext(ctx).Debugf("Disconnected %s", grpcConn.Target())
	}()

	return conn, nil
}
