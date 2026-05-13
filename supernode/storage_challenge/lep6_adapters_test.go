package storage_challenge

import (
	"context"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/btcsuite/btcutil/base58"
	"github.com/stretchr/testify/require"
)

type fakeArtifactRetriever struct {
	data      []byte
	lastKey   string
	lastLocal []bool
}

func (f *fakeArtifactRetriever) Retrieve(_ context.Context, key string, localOnly ...bool) ([]byte, error) {
	f.lastKey = key
	f.lastLocal = append([]bool(nil), localOnly...)
	return append([]byte(nil), f.data...), nil
}

func artifactKeyForTest(t *testing.T, data []byte) string {
	t.Helper()
	hash, err := utils.Blake3Hash(data)
	require.NoError(t, err)
	return base58.Encode(hash)
}

func TestP2PArtifactReaderReadRangeRejectsContentHashMismatch(t *testing.T) {
	t.Parallel()

	stored := []byte("corrupted-symbol-bytes")
	original := []byte("original-symbol-bytes")
	key := artifactKeyForTest(t, original)
	reader := &p2pArtifactReader{p2p: &fakeArtifactRetriever{data: stored}}

	_, err := reader.ReadArtifactRange(context.Background(), audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, key, 0, 4)
	require.Error(t, err)
	require.Contains(t, err.Error(), "content hash mismatch")
}

func TestP2PArtifactReaderReadRangeReturnsVerifiedBytes(t *testing.T) {
	t.Parallel()

	stored := []byte("verified-symbol-bytes")
	key := artifactKeyForTest(t, stored)
	fake := &fakeArtifactRetriever{data: stored}
	reader := &p2pArtifactReader{p2p: fake}

	got, err := reader.ReadArtifactRange(context.Background(), audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, key, 3, 9)
	require.NoError(t, err)
	require.Equal(t, stored[3:9], got)
	require.Equal(t, key, fake.lastKey)
	require.Equal(t, []bool{true}, fake.lastLocal)
}

func TestP2PArtifactReaderArtifactSizeRejectsContentHashMismatch(t *testing.T) {
	t.Parallel()

	key := artifactKeyForTest(t, []byte("original-symbol-bytes"))
	reader := &p2pArtifactReader{p2p: &fakeArtifactRetriever{data: []byte("corrupted-symbol-bytes")}}

	_, err := reader.ArtifactSize(context.Background(), audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, key)
	require.Error(t, err)
	require.Contains(t, err.Error(), "content hash mismatch")
}
