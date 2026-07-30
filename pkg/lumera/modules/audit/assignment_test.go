package audit

import (
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func assignedResponse(epoch uint64, reporter string, logical, current []string) *audittypes.QueryAssignedTargetsResponse {
	mappings := make([]audittypes.AccountIdentityMapping, len(logical))
	for i := range logical {
		mappings[i] = audittypes.AccountIdentityMapping{LogicalAccount: logical[i], CurrentAccount: current[i]}
	}
	return &audittypes.QueryAssignedTargetsResponse{
		EpochId:                  epoch,
		ReporterSupernodeAccount: reporter,
		TargetSupernodeAccounts:  append([]string(nil), logical...),
		TargetAccountMappings:    mappings,
		RequiredOpenPorts:        []uint32{4444, 5555},
	}
}

func TestResolveAssignedTargetsMigrationMatrix(t *testing.T) {
	tests := []struct {
		name            string
		requested       string
		reporterLogical string
		targetLogical   string
		targetCurrent   string
	}{
		{name: "unmigrated", requested: "reporter-A", reporterLogical: "reporter-A", targetLogical: "target-A", targetCurrent: "target-A"},
		{name: "reporter-only", requested: "reporter-B", reporterLogical: "reporter-A", targetLogical: "target-A", targetCurrent: "target-A"},
		{name: "target-only", requested: "reporter-A", reporterLogical: "reporter-A", targetLogical: "target-A", targetCurrent: "target-B"},
		{name: "both", requested: "reporter-B", reporterLogical: "reporter-A", targetLogical: "target-A", targetCurrent: "target-B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := assignedResponse(17, tt.reporterLogical, []string{tt.targetLogical}, []string{tt.targetCurrent})
			got, err := ResolveAssignedTargets(resp, 17)
			require.NoError(t, err)
			require.Equal(t, tt.reporterLogical, got.ReporterAccount)
			require.Equal(t, []AssignedTarget{{LogicalAccount: tt.targetLogical, CurrentAccount: tt.targetCurrent}}, got.Targets)
			// The request account is deliberately independent: the adapter must
			// trust the chain's epoch-logical reporter projection.
			require.NotEmpty(t, tt.requested)
		})
	}
}

func TestResolveAssignedTargetsRejectsNextEpochAndMalformedMappings(t *testing.T) {
	valid := func() *audittypes.QueryAssignedTargetsResponse {
		return assignedResponse(21, "reporter-A", []string{"target-A", "target-C"}, []string{"target-B", "target-D"})
	}
	tests := []struct {
		name   string
		mutate func(*audittypes.QueryAssignedTargetsResponse)
		want   string
	}{
		{name: "next epoch", mutate: func(r *audittypes.QueryAssignedTargetsResponse) { r.EpochId = 22 }, want: "epoch mismatch"},
		{name: "missing reporter", mutate: func(r *audittypes.QueryAssignedTargetsResponse) { r.ReporterSupernodeAccount = " " }, want: "missing logical reporter"},
		{name: "mapping count", mutate: func(r *audittypes.QueryAssignedTargetsResponse) {
			r.TargetAccountMappings = r.TargetAccountMappings[:1]
		}, want: "mapping count mismatch"},
		{name: "mapping order", mutate: func(r *audittypes.QueryAssignedTargetsResponse) {
			r.TargetAccountMappings[0], r.TargetAccountMappings[1] = r.TargetAccountMappings[1], r.TargetAccountMappings[0]
		}, want: "logical account mismatch"},
		{name: "duplicate logical", mutate: func(r *audittypes.QueryAssignedTargetsResponse) {
			r.TargetSupernodeAccounts[1] = "target-A"
			r.TargetAccountMappings[1].LogicalAccount = "target-A"
		}, want: "duplicates logical"},
		{name: "duplicate current", mutate: func(r *audittypes.QueryAssignedTargetsResponse) {
			r.TargetAccountMappings[1].CurrentAccount = "target-B"
		}, want: "aliases current"},
		{name: "empty account", mutate: func(r *audittypes.QueryAssignedTargetsResponse) { r.TargetAccountMappings[0].CurrentAccount = " " }, want: "empty account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := valid()
			tt.mutate(resp)
			_, err := ResolveAssignedTargets(resp, 21)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
