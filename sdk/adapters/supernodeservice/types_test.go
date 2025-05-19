package supernodeservice

import (
	"testing"

	"github.com/LumeraProtocol/supernode/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/sdk/event"
	"github.com/stretchr/testify/require"
)

func TestTranslateSupernodeEvent(t *testing.T) {
	tests := []struct {
		name     string
		input    cascade.SupernodeEventType
		expected event.EventType
	}{
		{"ACTION_RETRIEVED", cascade.SupernodeEventType_ACTION_RETRIEVED, event.SupernodeActionRetrieved},
		{"ACTION_FEE_VERIFIED", cascade.SupernodeEventType_ACTION_FEE_VERIFIED, event.SupernodeActionFeeVerified},
		{"TOP_SUPERNODE_CHECK_PASSED", cascade.SupernodeEventType_TOP_SUPERNODE_CHECK_PASSED, event.SupernodeTopCheckPassed},
		{"METADATA_DECODED", cascade.SupernodeEventType_METADATA_DECODED, event.SupernodeMetadataDecoded},
		{"DATA_HASH_VERIFIED", cascade.SupernodeEventType_DATA_HASH_VERIFIED, event.SupernodeDataHashVerified},
		{"INPUT_ENCODED", cascade.SupernodeEventType_INPUT_ENCODED, event.SupernodeInputEncoded},
		{"SIGNATURE_VERIFIED", cascade.SupernodeEventType_SIGNATURE_VERIFIED, event.SupernodeSignatureVerified},
		{"RQID_GENERATED", cascade.SupernodeEventType_RQID_GENERATED, event.SupernodeRQIDGenerated},
		{"RQID_VERIFIED", cascade.SupernodeEventType_RQID_VERIFIED, event.SupernodeRQIDVerified},
		{"ARTEFACTS_STORED", cascade.SupernodeEventType_ARTEFACTS_STORED, event.SupernodeArtefactsStored},
		{"ACTION_FINALIZED", cascade.SupernodeEventType_ACTION_FINALIZED, event.SupernodeActionFinalized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := toSdkEvent(tt.input)
			require.Equal(t, tt.expected, actual)
		})
	}
}
