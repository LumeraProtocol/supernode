package storage_challenge

import (
	"context"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	actionmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	"github.com/cosmos/gogoproto/proto"
	"go.uber.org/mock/gomock"
)

func TestChainTicketProviderFiltersFinalizedCascadeActions(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := lumeraMock.NewMockClient(ctrl)
	actions := actionmod.NewMockModule(ctrl)

	metadata := validCascadeMetadata(t)
	client.EXPECT().Action().Return(actions).Times(2)
	actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateDone).Return(&actiontypes.QueryListActionsResponse{Actions: []*actiontypes.Action{
		{ActionID: "sym-old", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 99, SuperNodes: []string{"sn-action-top"}, Metadata: metadata},
		{ActionID: "sym-old", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 99, SuperNodes: []string{"sn-action-top"}, Metadata: metadata}, // duplicate
		{ActionID: "wrong-type", ActionType: actiontypes.ActionTypeSense, State: actiontypes.ActionStateDone, BlockHeight: 102, SuperNodes: []string{"sn-action-top"}, Metadata: metadata},
		{ActionID: "zero-height", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 0, SuperNodes: []string{"sn-action-top"}, Metadata: metadata},
		{ActionID: "bad-metadata", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateDone, BlockHeight: 104, SuperNodes: []string{"sn-action-top"}, Metadata: []byte("not-proto")},
	}}, nil)
	actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateApproved).Return(&actiontypes.QueryListActionsResponse{Actions: []*actiontypes.Action{
		{ActionID: "sym-approved", ActionType: actiontypes.ActionTypeCascade, State: actiontypes.ActionStateApproved, BlockHeight: 100, SuperNodes: []string{"sn-action-top"}, Metadata: metadata},
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

func validCascadeMetadata(t *testing.T) []byte {
	t.Helper()
	bz, err := proto.Marshal(&actiontypes.CascadeMetadata{
		DataHash:            "hash",
		RqIdsMax:            3,
		RqIdsIds:            []string{"rq-1"},
		IndexArtifactCount:  1,
		SymbolArtifactCount: 1,
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return bz
}
