package audit

import (
	"fmt"
	"strings"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

// AssignedTarget separates the identity frozen into the epoch assignment from
// the account that currently owns and serves that identity.
type AssignedTarget struct {
	LogicalAccount string
	CurrentAccount string
}

// AssignedTargets is the validated, continuity-aware projection of the chain
// response. Logical accounts are used in reports and deterministic transcripts;
// current accounts are used only for live network routing.
type AssignedTargets struct {
	EpochID           uint64
	ReporterAccount   string
	RequiredOpenPorts []uint32
	Targets           []AssignedTarget
}

// ResolveAssignedTargets validates the chain-provided identity mappings. It
// deliberately does not consult SuperNode.PrevSupernodeAccounts: only Audit's
// indexed lineage is authoritative for an epoch assignment.
func ResolveAssignedTargets(resp *audittypes.QueryAssignedTargetsResponse, requestedEpoch uint64) (AssignedTargets, error) {
	if resp == nil {
		return AssignedTargets{}, fmt.Errorf("assigned targets response is nil")
	}
	if resp.EpochId != requestedEpoch {
		return AssignedTargets{}, fmt.Errorf("assigned targets epoch mismatch: got %d, want %d", resp.EpochId, requestedEpoch)
	}
	reporter := strings.TrimSpace(resp.ReporterSupernodeAccount)
	if reporter == "" {
		return AssignedTargets{}, fmt.Errorf("assigned targets response is missing logical reporter")
	}
	if len(resp.TargetAccountMappings) != len(resp.TargetSupernodeAccounts) {
		return AssignedTargets{}, fmt.Errorf("assigned targets mapping count mismatch: got %d mappings for %d targets", len(resp.TargetAccountMappings), len(resp.TargetSupernodeAccounts))
	}

	resolved := AssignedTargets{
		EpochID:           resp.EpochId,
		ReporterAccount:   reporter,
		RequiredOpenPorts: append([]uint32(nil), resp.RequiredOpenPorts...),
		Targets:           make([]AssignedTarget, len(resp.TargetSupernodeAccounts)),
	}
	seenLogical := make(map[string]struct{}, len(resolved.Targets))
	seenCurrent := make(map[string]struct{}, len(resolved.Targets))
	for i, expected := range resp.TargetSupernodeAccounts {
		logical := strings.TrimSpace(resp.TargetAccountMappings[i].LogicalAccount)
		current := strings.TrimSpace(resp.TargetAccountMappings[i].CurrentAccount)
		if logical == "" || current == "" {
			return AssignedTargets{}, fmt.Errorf("assigned target mapping %d has an empty account", i)
		}
		if logical != strings.TrimSpace(expected) {
			return AssignedTargets{}, fmt.Errorf("assigned target mapping %d logical account mismatch: got %q, want %q", i, logical, expected)
		}
		if _, exists := seenLogical[logical]; exists {
			return AssignedTargets{}, fmt.Errorf("assigned target mapping duplicates logical account %q", logical)
		}
		if logical == reporter {
			return AssignedTargets{}, fmt.Errorf("assigned target mapping duplicates logical reporter %q", reporter)
		}
		if _, exists := seenCurrent[current]; exists {
			return AssignedTargets{}, fmt.Errorf("assigned target mapping aliases current account %q", current)
		}
		seenLogical[logical] = struct{}{}
		seenCurrent[current] = struct{}{}
		resolved.Targets[i] = AssignedTarget{LogicalAccount: logical, CurrentAccount: current}
	}
	return resolved, nil
}
