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

// lumeraClient implements the Client interface with refactored modules
type lumeraClient struct {
	cfg          *Config
	authMod      auth.Module
	actionMod    action.Module
	actionMsgMod action_msg.Module
	supernodeMod supernode.Module
	txMod        tx.Module // Enhanced with modular transaction capabilities
	nodeMod      node.Module
	conn         Connection
}

// newClient creates a new Lumera client with refactored modules
func newClient(ctx context.Context, cfg *Config) (Client, error) {

	// Create a single gRPC connection to be shared by all modules
	conn, err := newGRPCConnection(ctx, cfg.GRPCAddr)
	if err != nil {
		return nil, err
	}

	// Initialize enhanced tx module first (other modules depend on it)
	txModule, err := tx.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Initialize auth module (needed by action_msg and other modules)
	authModule, err := auth.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Initialize action module (independent)
	actionModule, err := action.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Initialize supernode module (independent)
	supernodeModule, err := supernode.NewModule(conn.GetConn())
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Initialize node module (independent)
	nodeModule, err := node.NewModule(conn.GetConn(), cfg.keyring)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Initialize action_msg module with enhanced dependencies
	// Now it uses the modular tx system for all transaction operations
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
		txMod:        txModule, // Now provides comprehensive transaction capabilities
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

// ActionMsg returns the ActionMsg module client (now simplified and focused)
func (c *lumeraClient) ActionMsg() action_msg.Module {
	return c.actionMsgMod
}

// SuperNode returns the SuperNode module client
func (c *lumeraClient) SuperNode() supernode.Module {
	return c.supernodeMod
}

// Tx returns the enhanced Transaction module client
// This now provides comprehensive transaction capabilities for all modules
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
