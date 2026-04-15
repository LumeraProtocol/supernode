package storage_challenge

import (
	"encoding/json"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/stretchr/testify/require"
)

func TestBuildStorageChallengeFailureEvidenceMetadata_NoMapPayload(t *testing.T) {
	meta := buildStorageChallengeFailureEvidenceMetadata(
		42,
		"lumera1challengerxxxxxxxxxxxxxxxxxxxx",
		"lumera1recipientxxxxxxxxxxxxxxxxxxxxx",
		"challenge-id-123",
		"file-key-abc",
		"INVALID_PROOF",
		"deadbeef",
	)

	require.Equal(t, uint64(42), meta.EpochId)
	require.Equal(t, "challenge-id-123", meta.ChallengeId)
	require.Equal(t, "file-key-abc", meta.FileKey)
	require.Equal(t, "INVALID_PROOF", meta.FailureType)
	require.Equal(t, "deadbeef", meta.TranscriptHash)

	bz, err := json.Marshal(meta)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(bz, &got))
	require.NotContains(t, got, "details")
	require.NotContains(t, got, "metadata")

	var roundtrip audittypes.StorageChallengeFailureEvidenceMetadata
	require.NoError(t, json.Unmarshal(bz, &roundtrip))
	require.Equal(t, meta, roundtrip)
}
