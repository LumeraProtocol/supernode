package lumera

import (
	"context"

	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/tx"
)

type lumeraClient struct {
	cfg          *Config
	authMod      auth.Module
	actionMod    action.Module
	actionMsgMod action_msg.Module
	supernodeMod supernode.Module
	txMod        tx.Module
	nodeMod      node.Module
	conn         Connection
}

func newClient(ctx context.Context, cfg *Config) (Client, error) {

	conn, err := newGRPCConnection(ctx, cfg.GRPCAddr)
	if err != nil {
		return nil, err
	}

	txModule, err := tx.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	authModule, err := auth.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	actionModule, err := action.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	supernodeModule, err := supernode.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	nodeModule, err := node.NewModule(conn.GetConn(), cfg.keyring)
	if err != nil {
		conn.Close()
		return nil, err
	}

	actionMsgModule, err := action_msg.NewModule(
		conn.GetConn(),
		authModule,  // For account info
		txModule,    // For transaction operations
		cfg.keyring, // For signing
		cfg.KeyName, // Key to use
		cfg.ChainID, // Chain configuration
	)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return &lumeraClient{
		cfg:          cfg,
		authMod:      authModule,
		actionMod:    actionModule,
		actionMsgMod: actionMsgModule,
		supernodeMod: supernodeModule,
		txMod:        txModule,
		nodeMod:      nodeModule,
		conn:         conn,
	}, nil
}

func (c *lumeraClient) Auth() auth.Module {
	return c.authMod
}

func (c *lumeraClient) Action() action.Module {
	return c.actionMod
}

func (c *lumeraClient) ActionMsg() action_msg.Module {
	return c.actionMsgMod
}

func (c *lumeraClient) SuperNode() supernode.Module {
	return c.supernodeMod
}

func (c *lumeraClient) Tx() tx.Module {
	return c.txMod
}

func (c *lumeraClient) Node() node.Module {
	return c.nodeMod
}

func (c *lumeraClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
