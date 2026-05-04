package storage_challenge

import (
	"context"
	"sort"
	"strings"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
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
	if p == nil || p.client == nil || p.client.Action() == nil {
		return nil, nil
	}
	target := strings.TrimSpace(targetSupernodeAccount)
	if target == "" {
		return nil, nil
	}

	resp, err := p.client.Action().ListActionsBySuperNode(ctx, target)
	if err != nil || resp == nil {
		return nil, err
	}

	out := make([]TicketDescriptor, 0, len(resp.Actions))
	seen := make(map[string]struct{}, len(resp.Actions))
	for _, act := range resp.Actions {
		if !isEligibleCascadeAction(act, target) {
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
		out = append(out, TicketDescriptor{TicketID: id, AnchorBlock: act.BlockHeight})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].TicketID < out[j].TicketID })
	return out, nil
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
	for _, sn := range act.SuperNodes {
		if strings.TrimSpace(sn) == target {
			return true
		}
	}
	return false
}
