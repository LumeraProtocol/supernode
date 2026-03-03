package storage_challenge

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/queries"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"
	"github.com/stretchr/testify/require"
	"lukechampine.com/blake3"
)

func TestGetSliceProofRejectsInvalidRange(t *testing.T) {
	t.Parallel()

	mockP2P := &testP2PClient{}
	data := make([]byte, 1024)
	mockP2P.data = data

	srv := NewServer("recipient-1", mockP2P, nil)
	resp, err := srv.GetSliceProof(context.Background(), &supernode.GetSliceProofRequest{
		ChallengeId:    "challenge-1",
		EpochId:        1,
		FileKey:        "file-key",
		RequestedStart: 10,
		RequestedEnd:   10,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Ok)
	require.Contains(t, resp.Error, "invalid requested range")
	require.Empty(t, resp.Slice)
	require.Equal(t, 1, mockP2P.retrieveCalls)
	require.Equal(t, "file-key", mockP2P.lastKey)
	require.Equal(t, []bool{true}, mockP2P.lastLocalOnly)
}

func TestGetSliceProofRejectsOutOfBoundsStart(t *testing.T) {
	t.Parallel()

	mockP2P := &testP2PClient{}
	data := make([]byte, 1024)
	mockP2P.data = data

	srv := NewServer("recipient-1", mockP2P, nil)
	resp, err := srv.GetSliceProof(context.Background(), &supernode.GetSliceProofRequest{
		ChallengeId:    "challenge-2",
		EpochId:        1,
		FileKey:        "file-key",
		RequestedStart: uint64(len(data)),
		RequestedEnd:   uint64(len(data) + 1),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Ok)
	require.Contains(t, resp.Error, "out of bounds")
	require.Empty(t, resp.Slice)
	require.Equal(t, 1, mockP2P.retrieveCalls)
	require.Equal(t, "file-key", mockP2P.lastKey)
	require.Equal(t, []bool{true}, mockP2P.lastLocalOnly)
}

func TestGetSliceProofClampsMaxServedSliceLength(t *testing.T) {
	t.Parallel()

	mockP2P := &testP2PClient{}
	data := make([]byte, 200_000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	mockP2P.data = data

	srv := NewServer("recipient-1", mockP2P, nil)
	resp, err := srv.GetSliceProof(context.Background(), &supernode.GetSliceProofRequest{
		ChallengeId:    "challenge-3",
		EpochId:        1,
		FileKey:        "file-key",
		RequestedStart: 0,
		RequestedEnd:   uint64(len(data)),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Ok)
	require.Equal(t, uint64(0), resp.Start)
	require.Equal(t, maxServedSliceBytes, resp.End)
	require.Len(t, resp.Slice, int(maxServedSliceBytes))

	sum := blake3.Sum256(data[:maxServedSliceBytes])
	require.Equal(t, hex.EncodeToString(sum[:]), resp.ProofHashHex)
	require.Equal(t, 1, mockP2P.retrieveCalls)
	require.Equal(t, "file-key", mockP2P.lastKey)
	require.Equal(t, []bool{true}, mockP2P.lastLocalOnly)
}

func TestGetSliceProofPersistsTrustedRecipientAndServedRange(t *testing.T) {
	t.Parallel()

	mockP2P := &testP2PClient{}
	data := make([]byte, 200_000)
	for i := range data {
		data[i] = byte(i % 251)
	}
	mockP2P.data = data

	store := &capturingStore{}
	srv := NewServer("recipient-actual", mockP2P, store)
	req := &supernode.GetSliceProofRequest{
		ChallengeId:    "challenge-4",
		EpochId:        1,
		FileKey:        "file-key",
		RequestedStart: 5,
		RequestedEnd:   uint64(len(data)),
		RecipientId:    "recipient-spoofed",
	}

	resp, err := srv.GetSliceProof(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Ok)
	require.Equal(t, int(maxServedSliceBytes), len(resp.Slice))

	require.Len(t, store.messages, 2)
	require.Equal(t, int(types.ChallengeMessageType), store.messages[0].MessageType)
	require.Equal(t, int(types.ResponseMessageType), store.messages[1].MessageType)

	var persistedChallenge types.MessageData
	err = json.Unmarshal(store.messages[0].Data, &persistedChallenge)
	require.NoError(t, err)
	require.Equal(t, "recipient-actual", persistedChallenge.RecipientID)
	require.Equal(t, int(resp.Start), persistedChallenge.Challenge.StartIndex)
	require.Equal(t, int(resp.End), persistedChallenge.Challenge.EndIndex)
	require.NotEqual(t, int(req.RequestedEnd), persistedChallenge.Challenge.EndIndex)

	var persistedResponse types.MessageData
	err = json.Unmarshal(store.messages[1].Data, &persistedResponse)
	require.NoError(t, err)
	require.Equal(t, "recipient-actual", persistedResponse.RecipientID)
	require.Equal(t, resp.ProofHashHex, persistedResponse.Response.Hash)
}

type testP2PClient struct {
	data          []byte
	err           error
	retrieveCalls int
	lastKey       string
	lastLocalOnly []bool
}

func (m *testP2PClient) Retrieve(_ context.Context, key string, localOnly ...bool) ([]byte, error) {
	m.retrieveCalls++
	m.lastKey = key
	m.lastLocalOnly = append([]bool(nil), localOnly...)
	return m.data, m.err
}

func (m *testP2PClient) BatchRetrieve(_ context.Context, _ []string, _ int, _ string, _ ...bool) (map[string][]byte, error) {
	return nil, nil
}

func (m *testP2PClient) BatchRetrieveStream(_ context.Context, _ []string, _ int32, _ string, _ func(base58Key string, data []byte) error, _ ...bool) (int32, error) {
	return 0, nil
}

func (m *testP2PClient) Store(_ context.Context, _ []byte, _ int) (string, error) {
	return "", nil
}

func (m *testP2PClient) StoreBatch(_ context.Context, _ [][]byte, _ int, _ string) error {
	return nil
}

func (m *testP2PClient) Delete(_ context.Context, _ string) error {
	return nil
}

func (m *testP2PClient) Stats(_ context.Context) (*p2p.StatsSnapshot, error) {
	return nil, nil
}

func (m *testP2PClient) NClosestNodes(_ context.Context, _ int, _ string, _ ...string) []string {
	return nil
}

func (m *testP2PClient) NClosestNodesWithIncludingNodeList(_ context.Context, _ int, _ string, _ []string, _ []string) []string {
	return nil
}

func (m *testP2PClient) LocalStore(_ context.Context, _ string, _ []byte) (string, error) {
	return "", nil
}

func (m *testP2PClient) DisableKey(_ context.Context, _ string) error {
	return nil
}

func (m *testP2PClient) EnableKey(_ context.Context, _ string) error {
	return nil
}

func (m *testP2PClient) GetLocalKeys(_ context.Context, _ *time.Time, _ time.Time) ([]string, error) {
	return nil, nil
}

type capturingStore struct {
	queries.LocalStoreInterface
	messages []types.StorageChallengeLogMessage
}

func (s *capturingStore) InsertStorageChallengeMessage(msg types.StorageChallengeLogMessage) error {
	s.messages = append(s.messages, msg)
	return nil
}
