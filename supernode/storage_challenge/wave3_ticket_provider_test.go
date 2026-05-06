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

// Wave 3 — M10 regression. Pre-Wave-3 the eligibility filter required BOTH
// IndexArtifactCount AND SymbolArtifactCount > 0, silently hiding INDEX-only
// or SYMBOL-only tickets from the dispatcher. Post-Wave-3 a ticket is
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
			actions.EXPECT().ListActionsBySuperNode(gomock.Any(), "sn-target").Return(
				&actiontypes.QueryListActionsBySuperNodeResponse{
					Actions: []*actiontypes.Action{{
						ActionID:    "sym-1",
						ActionType:  actiontypes.ActionTypeCascade,
						State:       actiontypes.ActionStateDone,
						BlockHeight: 100,
						SuperNodes:  []string{"sn-target"},
						Metadata:    metaBytes,
					}},
				}, nil)

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
