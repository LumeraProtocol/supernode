package supernode

import (
	"context"
	"fmt"

	lumerasn "github.com/LumeraProtocol/lumera/x/supernode/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type GetSupernodeRequest struct {
	ValidatorAddress string
}

func (c *Client) GetSupernodeByAddress(ctx context.Context, r GetSupernodeRequest) (Supernode, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "GetSupernodeByAddress",
		logtrace.FieldModule:  logtrace.ValueLumeraSDK,
		logtrace.FieldRequest: r,
	}
	logtrace.Info(ctx, "fetching supernode details", fields)

	resp, err := c.supernodeService.GetSuperNode(ctx, &lumerasn.QueryGetSuperNodeRequest{
		ValidatorAddress: r.ValidatorAddress,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to fetch supernode detail", fields)
		return Supernode{}, fmt.Errorf("failed to fetch lumera: %w", err)
	}

	logtrace.Info(ctx, "successfully fetched the supernode details", fields)

	return toSupernode(resp), nil
}

func toSupernode(sn *lumerasn.QueryGetSuperNodeResponse) Supernode {
	return Supernode{ValidatorAddress: sn.Supernode.ValidatorAddress,
		States:           mapStates(sn.Supernode.States),
		Evidence:         mapEvidence(sn.Supernode.Evidence),
		PrevIPAddresses:  mapIPAddressHistory(sn.Supernode.PrevIpAddresses),
		Version:          sn.Supernode.Version,
		Metrics:          mapMetrics(sn.Supernode.Metrics),
		SupernodeAccount: sn.Supernode.SupernodeAccount,
	}
}
