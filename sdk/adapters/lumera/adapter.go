package lumera

import (
	"context"
	"fmt"
	"sort"

	"github.com/LumeraProtocol/supernode/v2/sdk/log"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	lumeraclient "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/golang/protobuf/proto"
)

//go:generate mockery --name=Client --output=testutil/mocks --outpkg=mocks --filename=lumera_mock.go
type Client interface {
	AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error)
	GetAction(ctx context.Context, actionID string) (Action, error)
	GetSupernodes(ctx context.Context, height int64) ([]Supernode, error)
	GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*sntypes.SuperNode, error)
	GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error)
	DecodeCascadeMetadata(ctx context.Context, action Action) (actiontypes.CascadeMetadata, error)
	VerifySignature(ctx context.Context, accountAddr string, data []byte, signature []byte) error
}

// SuperNodeInfo contains supernode information with latest address
type SuperNodeInfo struct {
	SupernodeAccount string `json:"supernode_account"`
	ValidatorAddress string `json:"validator_address"`
	P2PPort          string `json:"p2p_port"`
	LatestAddress    string `json:"latest_address"`
	CurrentState     string `json:"current_state"`
}

// ConfigParams holds configuration parameters from global config
type ConfigParams struct {
	GRPCAddr string
	ChainID  string
	KeyName  string
	Keyring  keyring.Keyring
}

type Adapter struct {
	client lumeraclient.Client
	logger log.Logger
}

// NewAdapter creates a new Adapter with dependencies explicitly injected
func NewAdapter(ctx context.Context, config ConfigParams, logger log.Logger) (Client, error) {
	// Set default logger if nil
	if logger == nil {
		logger = log.NewNoopLogger()
	}

	lumeraConfig, err := lumeraclient.NewConfig(config.GRPCAddr, config.ChainID, config.KeyName, config.Keyring)
	if err != nil {
		logger.Error(ctx, "Failed to create Lumera config", "error", err)
		return nil, fmt.Errorf("failed to create Lumera config: %w", err)
	}
	// Initialize the client
	client, err := lumeraclient.NewClient(ctx, lumeraConfig)
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

func (a *Adapter) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*sntypes.SuperNode, error) {
	a.logger.Debug(ctx, "Getting supernode by address", "address", address)
	resp, err := a.client.SuperNode().GetSupernodeBySupernodeAddress(ctx, address)
	if err != nil {
		a.logger.Error(ctx, "Failed to get supernode", "address", address, "error", err)
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}
	if resp == nil {
		a.logger.Error(ctx, "Received nil response for supernode", "address", address)
		return nil, fmt.Errorf("received nil response for supernode %s", address)
	}
	a.logger.Debug(ctx, "Successfully retrieved supernode", "address", address)
	return resp, nil
}

func (a *Adapter) GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error) {
	a.logger.Debug(ctx, "Getting supernode with latest address", "address", address)

	resp, err := a.client.SuperNode().GetSupernodeBySupernodeAddress(ctx, address)
	if err != nil {
		a.logger.Error(ctx, "Failed to get supernode", "address", address, "error", err)
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}
	if resp == nil {
		a.logger.Error(ctx, "Received nil response for supernode", "address", address)
		return nil, fmt.Errorf("received nil response for supernode %s", address)
	}

	// Sort PrevIpAddresses by height in descending order
	sort.Slice(resp.PrevIpAddresses, func(i, j int) bool {
		return resp.PrevIpAddresses[i].Height > resp.PrevIpAddresses[j].Height
	})

	// Sort States by height in descending order
	sort.Slice(resp.States, func(i, j int) bool {
		return resp.States[i].Height > resp.States[j].Height
	})

	// Extract latest address
	latestAddress := ""
	if len(resp.PrevIpAddresses) > 0 {
		latestAddress = resp.PrevIpAddresses[0].Address
	}

	// Extract current state
	currentState := ""
	if len(resp.States) > 0 {
		currentState = resp.States[0].State.String()
	}

	info := &SuperNodeInfo{
		SupernodeAccount: resp.SupernodeAccount,
		ValidatorAddress: resp.ValidatorAddress,
		P2PPort:          resp.P2PPort,
		LatestAddress:    latestAddress,
		CurrentState:     currentState,
	}

	a.logger.Debug(ctx, "Successfully retrieved supernode with latest address",
		"address", address, "latestAddress", latestAddress, "currentState", currentState)
	return info, nil
}

func (a *Adapter) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	a.logger.Debug(ctx, "Getting account info by address", "address", addr)
	resp, err := a.client.Auth().AccountInfoByAddress(ctx, addr)
	if err != nil {
		a.logger.Error(ctx, "Failed to get account info", "address", addr, "error", err)
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}
	if resp == nil {
		a.logger.Error(ctx, "Received nil response for account info", "address", addr)
		return nil, fmt.Errorf("received nil response for account info %s", addr)
	}
	a.logger.Debug(ctx, "Successfully retrieved account info", "address", addr)

	return resp, nil
}

func (a *Adapter) GetAction(ctx context.Context, actionID string) (Action, error) {
	a.logger.Debug(ctx, "Getting action from blockchain", "actionID", actionID)

	resp, err := a.client.Action().GetAction(ctx, actionID)
	if err != nil {
		a.logger.Error(ctx, "Failed to get action", "actionID", actionID, "error", err)
		return Action{}, fmt.Errorf("failed to get action: %w", err)
	}

	// Add validation
	if resp == nil {
		return Action{}, fmt.Errorf("received nil response for action %s", actionID)
	}

	if resp.Action == nil {
		return Action{}, fmt.Errorf("action %s not found", actionID)
	}

	action := toSdkAction(resp)
	a.logger.Debug(ctx, "Successfully retrieved action", "actionID", action.ID,
		"state", action.State, "height", action.Height)

	return action, nil
}

func (a *Adapter) GetSupernodes(ctx context.Context, height int64) ([]Supernode, error) {
	a.logger.Debug(ctx, "Getting top supernodes for block", "height", height)

	// Safely convert int64 to uint64
	var blockHeight uint64
	if height < 0 {
		return nil, fmt.Errorf("invalid block height: %d", height)
	}
	blockHeight = uint64(height)

	resp, err := a.client.SuperNode().GetTopSuperNodesForBlock(ctx, blockHeight)
	if err != nil {
		a.logger.Error(ctx, "Failed to get supernodes", "height", height, "error", err)
		return nil, fmt.Errorf("failed to get supernodes: %w", err)
	}

	supernodes := toSdkSupernodes(resp)
	a.logger.Debug(ctx, "Successfully retrieved supernodes", "count", len(supernodes))

	return supernodes, nil
}

func (a *Adapter) VerifySignature(ctx context.Context, accountAddr string, data, signature []byte) error {

	err := a.client.Auth().Verify(ctx, accountAddr, data, signature)
	if err != nil {
		a.logger.Error(ctx, "Signature verification failed", "accountAddr", accountAddr, "error", err)
		return fmt.Errorf("signature verification failed: %w", err)
	}
	a.logger.Debug(ctx, "Signature verified successfully", "accountAddr", accountAddr)
	return nil
}

// DecodeCascadeMetadata decodes the raw metadata bytes into CascadeMetadata
func (a *Adapter) DecodeCascadeMetadata(ctx context.Context, action Action) (actiontypes.CascadeMetadata, error) {
	if action.ActionType != "ACTION_TYPE_CASCADE" {
		return actiontypes.CascadeMetadata{}, fmt.Errorf("action is not of type CASCADE, got %s", action.ActionType)
	}

	var meta actiontypes.CascadeMetadata
	if err := proto.Unmarshal(action.Metadata, &meta); err != nil {
		a.logger.Error(ctx, "Failed to unmarshal cascade metadata", "actionID", action.ID, "error", err)
		return meta, fmt.Errorf("failed to unmarshal cascade metadata: %w", err)
	}

	a.logger.Debug(ctx, "Successfully decoded cascade metadata", "actionID", action.ID)
	return meta, nil
}

func toSdkAction(resp *actiontypes.QueryGetActionResponse) Action {
	return Action{
		ID:             resp.Action.ActionID,
		State:          ACTION_STATE(resp.Action.State.String()),
		Height:         resp.Action.BlockHeight,
		ExpirationTime: resp.Action.ExpirationTime,
		ActionType:     resp.Action.ActionType.String(),
		Metadata:       resp.Action.Metadata,
		Creator:        resp.Action.Creator,
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

		// Get the latest state based on height
		latestState, err := getLatestState(sn)
		if err != nil {
			continue
		}

		// Check if the latest state is active
		if latestState.State.String() != string(SUPERNODE_STATE_ACTIVE) {
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

func getLatestState(supernode *sntypes.SuperNode) (*sntypes.SuperNodeStateRecord, error) {
	if supernode == nil {
		return nil, fmt.Errorf("supernode is nil")
	}

	// Check if the slice has elements before accessing it
	if len(supernode.States) == 0 {
		return nil, fmt.Errorf("no state history exists for the supernode")
	}

	// Sort by height in descending order to get the latest first
	sort.Slice(supernode.States, func(i, j int) bool {
		return supernode.States[i].Height > supernode.States[j].Height
	})

	// Access the latest state safely
	if supernode.States[0] == nil {
		return nil, fmt.Errorf("latest state in history is nil")
	}

	return supernode.States[0], nil
}

func getLatestIP(supernode *sntypes.SuperNode) (string, error) {
	if supernode == nil {
		return "", fmt.Errorf("supernode is nil")
	}

	// Check if the slice has elements before accessing it
	if len(supernode.PrevIpAddresses) == 0 {
		return "", fmt.Errorf("no ip history exists for the supernode")
	}

	// Sort by height in descending order to get the latest first
	sort.Slice(supernode.PrevIpAddresses, func(i, j int) bool {
		return supernode.PrevIpAddresses[i].Height > supernode.PrevIpAddresses[j].Height
	})

	// Access the latest IP address safely
	if supernode.PrevIpAddresses[0] == nil {
		return "", fmt.Errorf("latest IP address in history is nil")
	}

	return supernode.PrevIpAddresses[0].Address, nil
}
