package storage_challenge

import (
	"context"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	actionmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"go.uber.org/mock/gomock"
)

func TestChainTicketProviderFiltersFinalizedCascadeActions(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := lumeraMock.NewMockClient(ctrl)
	actions := actionmod.NewMockModule(ctrl)

	client.EXPECT().Action().Return(actions).Times(2)
	actions.EXPECT().ListActionsBySuperNode(gomock.Any(), "sn-target").Return(&actiontypes.QueryListActionsBySuperNodeResponse{Actions: []*actiontypes.Action{
		{ActionID: "sym-old", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 99, SuperNodes: []string{"sn-target"}},
		{ActionID: "sym-approved", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateApproved, BlockHeight: 100, SuperNodes: []string{"sn-target"}},
		{ActionID: "sym-old", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 99, SuperNodes: []string{"sn-target"}}, // duplicate
		{ActionID: "pending", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStatePending, BlockHeight: 101, SuperNodes: []string{"sn-target"}},
		{ActionID: "wrong-type", ActionType: actiontypes.ActionTypeSense, State: actiontypes.ActionStateDone, BlockHeight: 102, SuperNodes: []string{"sn-target"}},
		{ActionID: "wrong-target", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 103, SuperNodes: []string{"other"}},
		{ActionID: "zero-height", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 0, SuperNodes: []string{"sn-target"}},
	}}, nil)

	got, err := NewChainTicketProvider(client).TicketsForTarget(context.Background(), "sn-target")
	if err != nil {
		t.Fatalf("TicketsForTarget returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 eligible tickets, got %d: %#v", len(got), got)
	}
	if got[0].TicketID != "sym-approved" || got[0].AnchorBlock != 100 {
		t.Fatalf("first sorted ticket mismatch: %#v", got[0])
	}
	if got[1].TicketID != "sym-old" || got[1].AnchorBlock != 99 {
		t.Fatalf("second sorted ticket mismatch: %#v", got[1])
	}
}
