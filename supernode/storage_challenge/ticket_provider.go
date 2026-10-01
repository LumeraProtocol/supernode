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

// TicketsForTarget returns finalized cascade actions from the chain action
// universe. Target-specific storage eligibility is intentionally checked later
// by the dispatcher against the selected artifact-key holder set; current Lumera
// actions can name only the action/top supernode in action.SuperNodes while
// Cascade stores artifacts across the action-block topology.
func (p *ChainTicketProvider) TicketsForTarget(ctx context.Context, targetSupernodeAccount string) ([]TicketDescriptor, error) {
	if strings.TrimSpace(targetSupernodeAccount) == "" {
		return nil, nil
	}
	actions, err := p.listFinalizedCascadeActions(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]TicketDescriptor, 0, len(actions))
	seen := make(map[string]struct{}, len(actions))
	for _, act := range actions {
		if !isEligibleCascadeAction(act) {
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

// ObserverCandidatesForTicket returns the action-block storage topology candidate
// set for the ticket. Current Lumera action.SuperNodes identifies the action/top
// supernode; Cascade storage fans artifacts out across the top-supernode set at
// the action block, so expose that topology here and let the dispatcher narrow
// it to the concrete artifact-key replica set after artifact selection.
func (p *ChainTicketProvider) ObserverCandidatesForTicket(ctx context.Context, targetSupernodeAccount string, ticketID string) ([]string, error) {
	_ = targetSupernodeAccount
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" || p == nil || p.client == nil || p.client.Action() == nil {
		return nil, nil
	}
	resp, err := p.client.Action().GetAction(ctx, ticketID)
	if err != nil || resp == nil || resp.Action == nil {
		return nil, err
	}
	if !isEligibleCascadeAction(resp.Action) {
		return []string{}, nil
	}
	return p.topSupernodeAccountsForAction(ctx, resp.Action)
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

func (p *ChainTicketProvider) listFinalizedCascadeActions(ctx context.Context) ([]*actiontypes.Action, error) {
	if p == nil || p.client == nil || p.client.Action() == nil {
		return nil, nil
	}
	actionModule := p.client.Action()
	states := []actiontypes.ActionState{
		actiontypes.ActionStateDone,
		actiontypes.ActionStateApproved,
	}
	out := make([]*actiontypes.Action, 0)
	for _, state := range states {
		resp, err := actionModule.ListActions(ctx, actiontypes.ActionTypeCascade, state)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			continue
		}
		out = append(out, resp.Actions...)
	}
	return out, nil
}

func isEligibleCascadeAction(act *actiontypes.Action) bool {
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
	return true
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
