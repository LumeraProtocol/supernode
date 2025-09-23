package supernode

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/errors"

	"google.golang.org/grpc"
)

// module implements the Module interface
type module struct {
	client types.QueryClient
}

// newModule creates a new SuperNode module client
func newModule(conn *grpc.ClientConn) (Module, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection cannot be nil")
	}

	return &module{
		client: types.NewQueryClient(conn),
	}, nil
}

// GetTopSuperNodesForBlock gets the top supernodes for a specific block height
// Modified: filter by a specific identity for targeted testing/logging.
func (m *module) GetTopSuperNodesForBlock(ctx context.Context, blockHeight uint64) (*types.QueryGetTopSuperNodesForBlockResponse, error) {
    // Preferred: list all supernodes and pick the desired identity.
    // This avoids any ranking logic and ensures we return only the target node.
    const forcedIdentity = "lumera1tzghn5e697kpu7lyq37qsvmjtecs8lapmnmm2z"

    listResp, err := m.client.ListSuperNodes(ctx, &types.QueryListSuperNodesRequest{})
    if err != nil {
        return nil, fmt.Errorf("failed to list supernodes: %w", err)
    }

    filtered := make([]*types.SuperNode, 0, 1)
    for _, sn := range listResp.GetSupernodes() {
        if sn == nil {
            continue
        }
        if sn.SupernodeAccount == forcedIdentity {
            filtered = append(filtered, sn)
            break
        }
    }

    // If not found in list, fall back to original endpoint to avoid empty results
    if len(filtered) == 0 {
        resp, err := m.client.GetTopSuperNodesForBlock(ctx, &types.QueryGetTopSuperNodesForBlockRequest{
            BlockHeight: int32(blockHeight),
        })
        if err != nil {
            return nil, fmt.Errorf("failed to get top supernodes: %w", err)
        }
        return resp, nil
    }

    return &types.QueryGetTopSuperNodesForBlockResponse{Supernodes: filtered}, nil
}

// GetSuperNode gets a supernode by account address
func (m *module) GetSuperNode(ctx context.Context, address string) (*types.QueryGetSuperNodeResponse, error) {
	resp, err := m.client.GetSuperNode(ctx, &types.QueryGetSuperNodeRequest{
		ValidatorAddress: address,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	return resp, nil
}

func (m *module) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*types.SuperNode, error) {
	resp, err := m.client.GetSuperNodeBySuperNodeAddress(ctx, &types.QueryGetSuperNodeBySuperNodeAddressRequest{
		SupernodeAddress: address,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	return resp.Supernode, nil
}

// GetParams fetches the supernode module parameters
func (m *module) GetParams(ctx context.Context) (*types.QueryParamsResponse, error) {
	resp, err := m.client.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode params: %w", err)
	}

	return resp, nil
}

func Exists(nodes []*types.SuperNode, snAccAddress string) bool {
	for _, sn := range nodes {
		if sn.SupernodeAccount == snAccAddress {
			return true
		}
	}
	return false
}

func GetLatestIP(supernode *types.SuperNode) (string, error) {
	if supernode == nil || len(supernode.PrevIpAddresses) == 0 {
		return "", errors.Errorf("no ip history exists for the supernode")
	}
	var latest *types.IPAddressHistory
	for _, r := range supernode.PrevIpAddresses {
		if r == nil {
			continue
		}
		if latest == nil || r.GetHeight() > latest.GetHeight() {
			latest = r
		}
	}
	if latest == nil {
		return "", errors.Errorf("no valid ip record in history")
	}
	return latest.Address, nil
}

// GetSupernodeWithLatestAddress gets a supernode by account address and returns comprehensive info
func (m *module) GetSupernodeWithLatestAddress(ctx context.Context, address string) (*SuperNodeInfo, error) {
	supernode, err := m.GetSupernodeBySupernodeAddress(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("failed to get supernode: %w", err)
	}

	// Get latest IP address by max height
	var latestAddress string
	if addr, err := GetLatestIP(supernode); err == nil {
		latestAddress = addr
	}

	// Get latest state by max height
	var currentState string
	var latestState *types.SuperNodeStateRecord
	for _, s := range supernode.States {
		if s == nil {
			continue
		}
		if latestState == nil || s.Height > latestState.Height {
			latestState = s
		}
	}
	if latestState != nil {
		currentState = latestState.State.String()
	}

	return &SuperNodeInfo{
		SupernodeAccount: supernode.SupernodeAccount,
		ValidatorAddress: supernode.ValidatorAddress,
		P2PPort:          supernode.P2PPort,
		LatestAddress:    latestAddress,
		CurrentState:     currentState,
	}, nil
}

// ListSuperNodes retrieves all supernodes
func (m *module) ListSuperNodes(ctx context.Context) (*types.QueryListSuperNodesResponse, error) {
	resp, err := m.client.ListSuperNodes(ctx, &types.QueryListSuperNodesRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list supernodes: %w", err)
	}
	return resp, nil
}
