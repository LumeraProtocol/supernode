package supernode

import (
	"context"
	"fmt"

	lumerasn "github.com/LumeraProtocol/lumera/x/supernode/types"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/pkg/net"
)

// SuperNodeState defines possible states of a supernode.
type SuperNodeState int32

const (
	SUPERNODE_STATE_UNSPECIFIED SuperNodeState = 0
	SUPERNODE_STATE_ACTIVE      SuperNodeState = 1
	SUPERNODE_STATE_DISABLED    SuperNodeState = 2
	SUPERNODE_STATE_STOPPED     SuperNodeState = 3
	SUPERNODE_STATE_PENALIZED   SuperNodeState = 4
)

// Supernode represents a validator's state and related information.
type Supernode struct {
	ValidatorAddress string                 `json:"validator_address"`
	States           []SuperNodeStateRecord `json:"states"`
	Evidence         []Evidence             `json:"evidence"`
	PrevIPAddresses  []IPAddressHistory     `json:"prev_ip_addresses"`
	Version          string                 `json:"version"`
	Metrics          MetricsAggregate       `json:"metrics"`
	SupernodeAccount string                 `json:"supernode_account"`
}

type Supernodes []Supernode

// IPAddressHistory records a validator's previous IP addresses.
type IPAddressHistory struct {
	Address string `json:"address"`
	Height  int64  `json:"height"`
}

// MetricsAggregate contains aggregated metrics for a validator.
type MetricsAggregate struct {
	Metrics     map[string]float64 `json:"metrics"`
	ReportCount uint64             `json:"report_count"`
	Height      int64              `json:"height"`
}

// Evidence represents reports of a validator's misconduct.
type Evidence struct {
	ReporterAddress  string `json:"reporter_address"`
	ValidatorAddress string `json:"validator_address"`
	ActionID         string `json:"action_id"`
	EvidenceType     string `json:"evidence_type"`
	Description      string `json:"description"`
	Severity         uint64 `json:"severity"`
	Height           int32  `json:"height"`
}

// SuperNodeStateRecord stores state transitions of a supernode.
type SuperNodeStateRecord struct {
	State  SuperNodeState `json:"state"`
	Height int64          `json:"height"`
}

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

	resp, err := c.supernodeService.GetTopSuperNodesForBlock(ctx, &lumerasn.QueryGetTopSuperNodesForBlockRequest{
		BlockHeight: r.BlockHeight,
		Limit:       r.Limit,
		State:       lumerasn.SuperNodeState(r.State).String(),
	})
	if err != nil {
		fields[logtrace.FieldError] = err.Error()
		logtrace.Error(ctx, "failed to fetch top supernodes", fields)
		return GetTopSupernodesForBlockResponse{}, fmt.Errorf("failed to fetch lumera: %w", err)
	}

	logtrace.Info(ctx, "successfully fetched top supernodes", fields)
	return toGetTopSNsForBlockResponse(resp), nil
}

func toGetTopSNsForBlockResponse(response *lumerasn.QueryGetTopSuperNodesForBlockResponse) GetTopSupernodesForBlockResponse {
	var sns Supernodes

	for _, sn := range response.Supernodes {
		sns = append(sns, Supernode{
			ValidatorAddress: sn.ValidatorAddress,
			States:           mapStates(sn.States),
			Evidence:         mapEvidence(sn.Evidence),
			PrevIPAddresses:  mapIPAddressHistory(sn.PrevIpAddresses),
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
func mapStates(states []*lumerasn.SuperNodeStateRecord) []SuperNodeStateRecord {
	var stateRecords []SuperNodeStateRecord
	for _, state := range states {
		stateRecords = append(stateRecords, SuperNodeStateRecord{
			State:  SuperNodeState(state.State),
			Height: state.Height,
		})
	}
	return stateRecords
}

// Helper function to map repeated Evidence
func mapEvidence(evidences []*lumerasn.Evidence) []Evidence {
	var evidenceList []Evidence
	for _, ev := range evidences {
		evidenceList = append(evidenceList, Evidence{
			ReporterAddress:  ev.ReporterAddress,
			ValidatorAddress: ev.ValidatorAddress,
			ActionID:         ev.ActionId,
			EvidenceType:     ev.EvidenceType,
			Description:      ev.Description,
			Severity:         ev.Severity,
			Height:           ev.Height,
		})
	}
	return evidenceList
}

// Helper function to map repeated IPAddressHistory
func mapIPAddressHistory(addresses []*lumerasn.IPAddressHistory) []IPAddressHistory {
	var ipHistory []IPAddressHistory
	for _, addr := range addresses {
		ipHistory = append(ipHistory, IPAddressHistory{
			Address: addr.Address,
			Height:  addr.Height,
		})
	}
	return ipHistory
}

// Helper function to map MetricsAggregate
func mapMetrics(metrics *lumerasn.MetricsAggregate) MetricsAggregate {
	if metrics == nil {
		return MetricsAggregate{}
	}

	convertedMetrics := make(map[string]float64)
	for key, val := range metrics.Metrics {
		convertedMetrics[key] = val
	}

	return MetricsAggregate{
		Metrics:     convertedMetrics,
		ReportCount: metrics.ReportCount,
		Height:      metrics.Height,
	}
}
