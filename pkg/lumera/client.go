package lumera

import (
	"context"

	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/action_msg"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/auth"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/node"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/pkg/lumera/modules/tx"
)

// lumeraClient implements the Client interface
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

// newClient creates a new Lumera client with provided options
func newClient(ctx context.Context, cfg *Config) (Client, error) {

	// Create a single gRPC connection to be shared by all modules
	conn, err := newGRPCConnection(ctx, cfg.GRPCAddr)
	if err != nil {
		return nil, err
	}

	// Initialize all module clients with the shared connection
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

	actionMsgModule, err := action_msg.NewModule(
		conn.GetConn(),
		cfg.keyring,
		cfg.KeyName,
		cfg.ChainID,
	)
	if err != nil {
		conn.Close()
		return nil, err
	}

	supernodeModule, err := supernode.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	txModule, err := tx.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	nodeModule, err := node.NewModule(conn.GetConn(), cfg.keyring)
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

// Auth returns the Auth module client
func (c *lumeraClient) Auth() auth.Module {
	return c.authMod
}

// Action returns the Action module client
func (c *lumeraClient) Action() action.Module {
	return c.actionMod
}

// ActionMsg returns the ActionMsg module client
func (c *lumeraClient) ActionMsg() action_msg.Module {
	return c.actionMsgMod
}

// SuperNode returns the SuperNode module client
func (c *lumeraClient) SuperNode() supernode.Module {
	return c.supernodeMod
}

// Tx returns the Transaction module client
func (c *lumeraClient) Tx() tx.Module {
	return c.txMod
}

// Node returns the Node module client
func (c *lumeraClient) Node() node.Module {
	return c.nodeMod
}

// Close closes all connections
func (c *lumeraClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
