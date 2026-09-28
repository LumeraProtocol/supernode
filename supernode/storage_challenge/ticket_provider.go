package storage_challenge

import (
	"context"
	"sort"
	"strings"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	lep6metrics "github.com/LumeraProtocol/supernode/v2/pkg/metrics/lep6"
	"github.com/cosmos/gogoproto/proto"
)

// ChainTicketProvider discovers finalized cascade actions assigned to a target
// supernode via the final Lumera action query API. It is intentionally small:
// the dispatcher only needs ticket/action IDs and their register-time block
// heights for LEP-6 bucket classification.
type ChainTicketProvider struct {
	client lumera.Client
}

// NewChainTicketProvider constructs a production TicketProvider backed by
// x/action ListActionsBySuperNode.
func NewChainTicketProvider(client lumera.Client) *ChainTicketProvider {
	return &ChainTicketProvider{client: client}
}

// TicketsForTarget returns finalized cascade actions that include the target
// supernode in their action.SuperNodes assignment list.
func (p *ChainTicketProvider) TicketsForTarget(ctx context.Context, targetSupernodeAccount string) ([]TicketDescriptor, error) {
	resp, target, err := p.listActionsForTarget(ctx, targetSupernodeAccount)
	if err != nil || resp == nil {
		return nil, err
	}

	out := make([]TicketDescriptor, 0, len(resp.Actions))
	seen := make(map[string]struct{}, len(resp.Actions))
	for _, act := range resp.Actions {
		if !isEligibleCascadeAction(act, target) {
			lep6metrics.IncTicketDiscovery("ineligible")
			continue
		}
		id := strings.TrimSpace(act.ActionID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		lep6metrics.IncTicketDiscovery("eligible")
		out = append(out, TicketDescriptor{TicketID: id, AnchorBlock: act.BlockHeight})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].TicketID < out[j].TicketID })
	return out, nil
}

// ObserverCandidatesForTicket returns the expected storage replica set for the
// ticket. Current Lumera action.SuperNodes identifies the action/top supernode;
// Cascade storage fans artifacts out across the top-supernode set at the action
// block, so use that same current-chain query for LEP-6 observer candidates.
func (p *ChainTicketProvider) ObserverCandidatesForTicket(ctx context.Context, targetSupernodeAccount string, ticketID string) ([]string, error) {
	resp, target, err := p.listActionsForTarget(ctx, targetSupernodeAccount)
	if err != nil || resp == nil {
		return nil, err
	}
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return nil, nil
	}
	for _, act := range resp.Actions {
		if strings.TrimSpace(act.GetActionID()) != ticketID {
			continue
		}
		if !isEligibleCascadeAction(act, target) {
			return []string{}, nil
		}
		return p.topSupernodeAccountsForAction(ctx, act)
	}
	return []string{}, nil
}

func (p *ChainTicketProvider) topSupernodeAccountsForAction(ctx context.Context, act *actiontypes.Action) ([]string, error) {
	if p == nil || p.client == nil || p.client.SuperNode() == nil || act == nil || act.BlockHeight <= 0 {
		return nil, nil
	}
	resp, err := p.client.SuperNode().GetTopSuperNodesForBlock(ctx, &sntypes.QueryGetTopSuperNodesForBlockRequest{
		BlockHeight: int32(act.BlockHeight),
		State:       "SUPERNODE_STATE_ACTIVE",
		Limit:       10,
	})
	if err != nil || resp == nil {
		return nil, err
	}
	accounts := make([]string, 0, len(resp.Supernodes))
	for _, sn := range resp.Supernodes {
		if sn == nil {
			continue
		}
		accounts = append(accounts, sn.SupernodeAccount)
	}
	return uniqueNonEmptyStrings(accounts), nil
}

func (p *ChainTicketProvider) listActionsForTarget(ctx context.Context, targetSupernodeAccount string) (*actiontypes.QueryListActionsBySuperNodeResponse, string, error) {
	if p == nil || p.client == nil || p.client.Action() == nil {
		return nil, "", nil
	}
	target := strings.TrimSpace(targetSupernodeAccount)
	if target == "" {
		return nil, "", nil
	}
	resp, err := p.client.Action().ListActionsBySuperNode(ctx, target)
	if err != nil || resp == nil {
		return nil, target, err
	}
	return resp, target, nil
}

func isEligibleCascadeAction(act *actiontypes.Action, target string) bool {
	if act == nil {
		return false
	}
	if act.ActionType != actiontypes.ActionTypeCascade {
		return false
	}
	// LEP-6 challenges storage only after cascade finalization. Lumera marks
	// finalized/approved actions as DONE/APPROVED depending on the workflow
	// phase; reject pending/processing/rejected/failed/expired actions.
	if act.State != actiontypes.ActionStateDone && act.State != actiontypes.ActionStateApproved {
		return false
	}
	if act.BlockHeight <= 0 {
		return false
	}
	if !hasValidCascadeMetadata(act.Metadata) {
		return false
	}
	for _, sn := range act.SuperNodes {
		if strings.TrimSpace(sn) == target {
			return true
		}
	}
	return false
}

func hasValidCascadeMetadata(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var meta actiontypes.CascadeMetadata
	if err := proto.Unmarshal(raw, &meta); err != nil {
		return false
	}
	if strings.TrimSpace(meta.DataHash) == "" {
		return false
	}
	if meta.RqIdsMax == 0 || len(meta.RqIdsIds) == 0 {
		return false
	}
	// LEP-6 challenges tickets when at least one artifact class has a concrete
	// universe. SelectArtifactClass applies the §10 fallback when the rolled class
	// is empty; both zero remains invisible because no chain-acceptable proof row
	// can identify a concrete artifact.
	if meta.IndexArtifactCount == 0 && meta.SymbolArtifactCount == 0 {
		return false
	}
	return true
}

func uniqueNonEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
