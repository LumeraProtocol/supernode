package supernode

import (
	"context"
	"fmt"

	lumerasn "github.com/LumeraProtocol/lumera/x/supernode/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type GetSupernodeBySuperNodeAddressRequest struct {
	SupernodeAddress string
}

func (c *Client) GetSupernodeBySupernodeAddress(ctx context.Context, r GetSupernodeBySuperNodeAddressRequest) (Supernode, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:  "GetSupernodeBySupernodeAddress",
		logtrace.FieldModule:  logtrace.ValueLumeraSDK,
		logtrace.FieldRequest: r,
	}
	logtrace.Info(ctx, "fetching supernode details", fields)

	resp, err := c.supernodeService.GetSuperNodeBySuperNodeAddress(ctx, &lumerasn.QueryGetSuperNodeBySuperNodeAddressRequest{
		SupernodeAddress: r.SupernodeAddress,
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to fetch supernode detail", fields)
		return Supernode{}, fmt.Errorf("failed to fetch lumera: %w", err)
	}

	logtrace.Info(ctx, "successfully fetched the supernode details", fields)

	return toSuperNodeBySuperNodeAddressResponse(resp), nil
}

func toSuperNodeBySuperNodeAddressResponse(sn *lumerasn.QueryGetSuperNodeBySuperNodeAddressResponse) Supernode {
	return Supernode{ValidatorAddress: sn.Supernode.ValidatorAddress,
		States:           mapStates(sn.Supernode.States),
		Evidence:         mapEvidence(sn.Supernode.Evidence),
		PrevIPAddresses:  mapIPAddressHistory(sn.Supernode.PrevIpAddresses),
		Version:          sn.Supernode.Version,
		Metrics:          mapMetrics(sn.Supernode.Metrics),
		SupernodeAccount: sn.Supernode.SupernodeAccount,
	}
}
