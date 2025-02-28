package supernode

import (
	"context"
	"fmt"

	. "github.com/LumeraProtocol/lumera/x/supernode/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

type Supernodes []SuperNode

type GetTopSupernodesForBlockRequest struct {
	BlockHeight int32          `json:"block_height"`
	Limit       int32          `json:"limit"`
	State       SuperNodeState `json:"state"`
}

type GetTopSupernodesForBlockResponse struct {
	Supernodes Supernodes
}

func (c *Client) GetTopSNsByBlockHeight(ctx context.Context, r GetTopSupernodesForBlockRequest) (GetTopSupernodesForBlockResponse, error) {
	ctx = net.AddCorrelationID(ctx)

	fields := logtrace.Fields{
		logtrace.FieldMethod:         "GetTopSNsByBlockHeight",
		logtrace.FieldModule:         logtrace.ValueLumeraSDK,
		logtrace.FieldBlockHeight:    r.BlockHeight,
		logtrace.FieldLimit:          r.Limit,
		logtrace.FieldSupernodeState: r.State,
	}
	logtrace.Info(ctx, "fetching top supernodes for block", fields)

	resp, err := c.supernodeService.GetTopSuperNodesForBlock(ctx, &QueryGetTopSuperNodesForBlockRequest{
		BlockHeight: r.BlockHeight,
		Limit:       r.Limit,
		State:       r.State.String(),
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to fetch top supernodes", fields)
		return GetTopSupernodesForBlockResponse{}, fmt.Errorf("failed to fetch lumera: %w", err)
	}

	logtrace.Info(ctx, "successfully fetched top supernodes", fields)
	return toGetTopSNsForBlockResponse(resp), nil
}

func toGetTopSNsForBlockResponse(response *QueryGetTopSuperNodesForBlockResponse) GetTopSupernodesForBlockResponse {
	var sns Supernodes

	for _, sn := range response.Supernodes {
		sns = append(sns, SuperNode{
			ValidatorAddress: sn.ValidatorAddress,
			States:           mapStates(sn.States),
			Evidence:         mapEvidence(sn.Evidence),
			PrevIpAddresses:  mapIPAddressHistory(sn.PrevIpAddresses),
			Version:          sn.Version,
			Metrics:          mapMetrics(sn.Metrics),
			SupernodeAccount: sn.SupernodeAccount,
		})
	}

	return GetTopSupernodesForBlockResponse{
		Supernodes: sns,
	}
}

// Helper function to map repeated SuperNodeStateRecord
func mapStates(states []*SuperNodeStateRecord) []*SuperNodeStateRecord {
	var stateRecords []*SuperNodeStateRecord
	for _, state := range states {
		stateRecords = append(stateRecords, &SuperNodeStateRecord{
			State:  SuperNodeState(state.State),
			Height: state.Height,
		})
	}
	return stateRecords
}

// Helper function to map repeated Evidence
func mapEvidence(evidences []*Evidence) []*Evidence {
	var evidenceList []*Evidence
	for _, ev := range evidences {
		evidenceList = append(evidenceList, &Evidence{
			ReporterAddress:  ev.ReporterAddress,
			ValidatorAddress: ev.ValidatorAddress,
			ActionId:         ev.ActionId,
			EvidenceType:     ev.EvidenceType,
			Description:      ev.Description,
			Severity:         ev.Severity,
			Height:           ev.Height,
		})
	}
	return evidenceList
}

// Helper function to map repeated IPAddressHistory
func mapIPAddressHistory(addresses []*IPAddressHistory) []*IPAddressHistory {
	var ipHistory []*IPAddressHistory
	for _, addr := range addresses {
		ipHistory = append(ipHistory, &IPAddressHistory{
			Address: addr.Address,
			Height:  addr.Height,
		})
	}
	return ipHistory
}

// Helper function to map MetricsAggregate
func mapMetrics(metrics *MetricsAggregate) *MetricsAggregate {
	if metrics == nil {
		return &MetricsAggregate{}
	}

	convertedMetrics := make(map[string]float64)
	for key, val := range metrics.Metrics {
		convertedMetrics[key] = val
	}

	return &MetricsAggregate{
		Metrics:     convertedMetrics,
		ReportCount: metrics.ReportCount,
		Height:      metrics.Height,
	}
}
