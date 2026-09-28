package storage_challenge

import (
	"context"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	lumeraMock "github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	actionmod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/action"
	supernodemod "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/cosmos/gogoproto/proto"
	"go.uber.org/mock/gomock"
)

// LEP-6 M10 regression: the previous eligibility filter required BOTH
// IndexArtifactCount AND SymbolArtifactCount > 0, silently hiding INDEX-only
// or SYMBOL-only tickets from the dispatcher. After the fix a ticket is
// eligible if AT LEAST ONE class is non-zero. Both-zero remains invisible.
func TestChainTicketProvider_M10_AcceptsAtLeastOneClass(t *testing.T) {
	cases := []struct {
		name        string
		indexCount  uint32
		symbolCount uint32
		eligible    bool
	}{
		{"index_only", 1, 0, true},
		{"symbol_only", 0, 1, true},
		{"both", 1, 1, true},
		{"both_zero_legacy_invisible", 0, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			client := lumeraMock.NewMockClient(ctrl)
			actions := actionmod.NewMockModule(ctrl)

			meta := &actiontypes.CascadeMetadata{
				DataHash:            "h",
				RqIdsMax:            3,
				RqIdsIds:            []string{"rq-1"},
				IndexArtifactCount:  tc.indexCount,
				SymbolArtifactCount: tc.symbolCount,
			}
			metaBytes, err := proto.Marshal(meta)
			if err != nil {
				t.Fatalf("marshal meta: %v", err)
			}

			client.EXPECT().Action().Return(actions).Times(2)
			actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateDone).Return(
				&actiontypes.QueryListActionsResponse{
					Actions: []*actiontypes.Action{{
						ActionID:    "sym-1",
						ActionType:  actiontypes.ActionTypeCascade,
						State:       actiontypes.ActionStateDone,
						BlockHeight: 100,
						SuperNodes:  []string{"sn-action-top"},
						Metadata:    metaBytes,
					}},
				}, nil)
			actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateApproved).Return(
				&actiontypes.QueryListActionsResponse{}, nil)

			got, err := NewChainTicketProvider(client).TicketsForTarget(context.Background(), "sn-target")
			if err != nil {
				t.Fatalf("TicketsForTarget: %v", err)
			}
			gotEligible := len(got) == 1
			if gotEligible != tc.eligible {
				t.Fatalf("M10 regression: index=%d symbol=%d → eligible=%v want=%v",
					tc.indexCount, tc.symbolCount, gotEligible, tc.eligible)
			}
		})
	}
}

func TestChainTicketProvider_ObserverCandidatesUseActionBlockTopSupernodes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	client := lumeraMock.NewMockClient(ctrl)
	actions := actionmod.NewMockModule(ctrl)
	supernodes := supernodemod.NewMockModule(ctrl)

	meta := &actiontypes.CascadeMetadata{
		DataHash:            "h",
		RqIdsMax:            3,
		RqIdsIds:            []string{"rq-1"},
		IndexArtifactCount:  1,
		SymbolArtifactCount: 1,
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	actionResp := &actiontypes.QueryListActionsBySuperNodeResponse{Actions: []*actiontypes.Action{{
		ActionID:    "ticket-1",
		ActionType:  actiontypes.ActionTypeCascade,
		State:       actiontypes.ActionStateDone,
		BlockHeight: 75,
		// Current Lumera may store only the action/top supernode here; LEP-6
		// observers must come from the registration-time top-supernode topology.
		SuperNodes: []string{"sn-target"},
		Metadata:   metaBytes,
	}}}

	client.EXPECT().Action().Return(actions).Times(2)
	actions.EXPECT().GetAction(gomock.Any(), "ticket-1").Return(&actiontypes.QueryGetActionResponse{Action: actionResp.Actions[0]}, nil)
	client.EXPECT().SuperNode().Return(supernodes).Times(2)
	supernodes.EXPECT().GetTopSuperNodesForBlock(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *sntypes.QueryGetTopSuperNodesForBlockRequest) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
			if req.GetBlockHeight() != 75 || req.GetState() != "SUPERNODE_STATE_ACTIVE" {
				t.Fatalf("unexpected top-supernode request: %#v", req)
			}
			return &sntypes.QueryGetTopSuperNodesForBlockResponse{Supernodes: []*sntypes.SuperNode{
				{SupernodeAccount: "sn-target"},
				{SupernodeAccount: "sn-observer-a"},
				{SupernodeAccount: "sn-observer-b"},
			}}, nil
		})

	got, err := NewChainTicketProvider(client).ObserverCandidatesForTicket(context.Background(), "sn-target", "ticket-1")
	if err != nil {
		t.Fatalf("ObserverCandidatesForTicket: %v", err)
	}
	want := []string{"sn-observer-a", "sn-observer-b", "sn-target"}
	if len(got) != len(want) {
		t.Fatalf("candidates len=%d want=%d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates[%d]=%q want %q; all=%#v", i, got[i], want[i], got)
		}
	}
}

func TestChainTicketProvider_TicketsForTargetDiscoversActionTopSupernodeTicket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	client := lumeraMock.NewMockClient(ctrl)
	actions := actionmod.NewMockModule(ctrl)

	meta := &actiontypes.CascadeMetadata{
		DataHash:            "h",
		RqIdsMax:            3,
		RqIdsIds:            []string{"rq-1"},
		IndexArtifactCount:  1,
		SymbolArtifactCount: 1,
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	// Current Lumera can finalize an action with only the action/top supernode in
	// action.SuperNodes. LEP-6 challenge eligibility is decided later by the
	// artifact-key holder set, so discovery must not be keyed only by the epoch
	// target's action.SuperNodes membership.
	client.EXPECT().Action().Return(actions).Times(2)
	actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateDone).Return(
		&actiontypes.QueryListActionsResponse{Actions: []*actiontypes.Action{{
			ActionID:    "ticket-action-top-only",
			ActionType:  actiontypes.ActionTypeCascade,
			State:       actiontypes.ActionStateDone,
			BlockHeight: 83,
			SuperNodes:  []string{"sn-action-top"},
			Metadata:    metaBytes,
		}}}, nil)
	actions.EXPECT().ListActions(gomock.Any(), actiontypes.ActionTypeCascade, actiontypes.ActionStateApproved).Return(
		&actiontypes.QueryListActionsResponse{}, nil)

	got, err := NewChainTicketProvider(client).TicketsForTarget(context.Background(), "sn-artifact-holder-target")
	if err != nil {
		t.Fatalf("TicketsForTarget: %v", err)
	}
	if len(got) != 1 || got[0].TicketID != "ticket-action-top-only" || got[0].AnchorBlock != 83 {
		t.Fatalf("TicketsForTarget()=%#v, want finalized action-top-only ticket", got)
	}
}
