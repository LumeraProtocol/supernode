package cascade

import (
	"testing"
)

func TestSupernodeEventTypeValues(t *testing.T) {
	tests := []struct {
		name     string
		value    SupernodeEventType
		expected int
	}{
		{"UNKNOWN", SupernodeEventTypeUNKNOWN, 0},
		{"ActionRetrieved", SupernodeEventTypeActionRetrieved, 1},
		{"ActionFeeVerified", SupernodeEventTypeActionFeeVerified, 2},
		{"TopSupernodeCheckPassed", SupernodeEventTypeTopSupernodeCheckPassed, 3},
		{"MetadataDecoded", SupernodeEventTypeMetadataDecoded, 4},
		{"DataHashVerified", SupernodeEventTypeDataHashVerified, 5},
		{"InputEncoded", SupernodeEventTypeInputEncoded, 6},
		{"SignatureVerified", SupernodeEventTypeSignatureVerified, 7},
		{"RQIDsGenerated", SupernodeEventTypeRQIDsGenerated, 8},
		{"RqIDsVerified", SupernodeEventTypeRqIDsVerified, 9},
		{"FinalizeSimulated", SupernodeEventTypeFinalizeSimulated, 10},
		{"ArtefactsStored", SupernodeEventTypeArtefactsStored, 11},
		{"ActionFinalized", SupernodeEventTypeActionFinalized, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if int(tt.value) != tt.expected {
				t.Errorf("Expected %s to be %d, got %d", tt.name, tt.expected, tt.value)
			}
		})
	}
}
