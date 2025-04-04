package lumera

import (
	"context"
	"fmt"

	"action/log"

	"github.com/LumeraProtocol/lumera/x/action/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/types"
	lumeraclient "github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

type Client interface {
	GetAction(ctx context.Context, actionID string) (Action, error)
	GetSupernodes(ctx context.Context, height int64) ([]Supernode, error)
}

// ConfigParams holds configuration parameters from global config
type ConfigParams struct {
	GRPCAddr string
	ChainID  string
	Timeout  int
}

type Adapter struct {
	client lumeraclient.Client
	logger log.Logger
}

// NewAdapter creates a new Adapter with dependencies explicitly injected
func NewAdapter(
	ctx context.Context,
	config ConfigParams,
	kr keyring.Keyring,
	logger log.Logger,
) (Client, error) {
	// Set default logger if nil
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	logger.Debug(ctx, "Creating Lumera adapter",
		"grpcAddr", config.GRPCAddr,
		"chainID", config.ChainID,
		"timeout", config.Timeout)

	// Create client options from the config
	options := []lumeraclient.Option{
		lumeraclient.WithGRPCAddr(config.GRPCAddr),
	}

	if config.ChainID != "" {
		options = append(options, lumeraclient.WithChainID(config.ChainID))
	}

	if config.Timeout > 0 {
		options = append(options, lumeraclient.WithTimeout(config.Timeout))
	}

	if kr != nil {
		options = append(options, lumeraclient.WithKeyring(kr))
	}

	// Initialize the client
	client, err := lumeraclient.NewClient(ctx, options...)
	if err != nil {
		logger.Error(ctx, "Failed to initialize Lumera client", "error", err)
		return nil, fmt.Errorf("failed to initialize Lumera client: %w", err)
	}

	logger.Info(ctx, "Lumera adapter created successfully")

	return &Adapter{
		client: client,
		logger: logger,
	}, nil
}

func (a *Adapter) GetAction(ctx context.Context, actionID string) (Action, error) {
	a.logger.Debug(ctx, "Getting action from blockchain", "actionID", actionID)

	resp, err := a.client.Action().GetAction(ctx, actionID)
	if err != nil {
		a.logger.Error(ctx, "Failed to get action", "actionID", actionID, "error", err)
		return Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	action := toSdkAction(resp)
	a.logger.Debug(ctx, "Successfully retrieved action",
		"actionID", action.ID,
		"state", action.State,
		"height", action.Height)

	return action, nil
}

func (a *Adapter) GetSupernodes(ctx context.Context, height int64) ([]Supernode, error) {
	a.logger.Debug(ctx, "Getting top supernodes for block", "height", height)

	resp, err := a.client.SuperNode().GetTopSuperNodesForBlock(ctx, uint64(height))
	if err != nil {
		a.logger.Error(ctx, "Failed to get supernodes", "height", height, "error", err)
		return nil, fmt.Errorf("failed to get supernodes: %w", err)
	}

	supernodes := toSdkSupernodes(resp)
	a.logger.Debug(ctx, "Successfully retrieved supernodes", "count", len(supernodes))

	return supernodes, nil
}

func toSdkAction(resp *types.QueryGetActionResponse) Action {
	return Action{
		ID:             resp.Action.ActionID,
		State:          ACTION_STATE(resp.Action.State),
		Height:         int64(resp.Action.BlockHeight),
		ExpirationTime: resp.Action.ExpirationTime,
	}
}

func toSdkSupernodes(resp *sntypes.QueryGetTopSuperNodesForBlockResponse) []Supernode {
	var result []Supernode
	for _, sn := range resp.Supernodes {
		ipAddress, err := getLatestIP(sn)
		if err != nil {
			continue
		}

		if sn.SupernodeAccount == "" {
			continue
		}

		if len(sn.States) == 0 {
			continue
		}

		if sn.States[0].State.String() != string(SUPERNODE_STATE_ACTIVE) {
			continue
		}

		result = append(result, Supernode{
			CosmosAddress: sn.SupernodeAccount,
			GrpcEndpoint:  ipAddress,
			State:         SUPERNODE_STATE_ACTIVE,
		})
	}
	return result
}

func getLatestIP(supernode *sntypes.SuperNode) (string, error) {
	if supernode == nil {
		return "", fmt.Errorf("supernode is nil")
	}

	if len(supernode.PrevIpAddresses) == 0 {
		return "", fmt.Errorf("no ip history exists for the supernode")
	}

	return supernode.PrevIpAddresses[0].Address, nil
}
