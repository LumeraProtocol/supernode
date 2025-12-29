package adaptors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type stubP2PClient struct {
	storeBatchCalls int
	lastTaskID      string
	lastType        int
}

var _ p2p.Client = (*stubP2PClient)(nil)

func (s *stubP2PClient) Retrieve(context.Context, string, ...bool) ([]byte, error) {
	panic("not implemented")
}
func (s *stubP2PClient) BatchRetrieve(context.Context, []string, int, string, ...bool) (map[string][]byte, error) {
	panic("not implemented")
}
func (s *stubP2PClient) BatchRetrieveStream(context.Context, []string, int32, string, func(string, []byte) error, ...bool) (int32, error) {
	panic("not implemented")
}
func (s *stubP2PClient) Store(context.Context, []byte, int) (string, error) { panic("not implemented") }
func (s *stubP2PClient) StoreBatch(_ context.Context, _ [][]byte, typ int, taskID string) error {
	s.storeBatchCalls++
	s.lastType = typ
	s.lastTaskID = taskID
	return nil
}
func (s *stubP2PClient) Delete(context.Context, string) error { panic("not implemented") }
func (s *stubP2PClient) Stats(context.Context) (map[string]interface{}, error) {
	panic("not implemented")
}
func (s *stubP2PClient) NClosestNodes(context.Context, int, string, ...string) []string {
	panic("not implemented")
}
func (s *stubP2PClient) NClosestNodesWithIncludingNodeList(context.Context, int, string, []string, []string) []string {
	panic("not implemented")
}
func (s *stubP2PClient) LocalStore(context.Context, string, []byte) (string, error) {
	panic("not implemented")
}
func (s *stubP2PClient) DisableKey(context.Context, string) error { panic("not implemented") }
func (s *stubP2PClient) EnableKey(context.Context, string) error  { panic("not implemented") }
func (s *stubP2PClient) GetLocalKeys(context.Context, *time.Time, time.Time) ([]string, error) {
	panic("not implemented")
}

func TestStoreCascadeSymbolsAndData_UpdatesFirstBatchByTaskID(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	taskID := "task123"
	actionID := "action456"

	symbolsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(symbolsDir, "sym1.bin"), []byte("sym"), 0600))

	p2pClient := &stubP2PClient{}

	store := rqstore.NewMockStore(ctrl)
	store.EXPECT().StoreSymbolDirectory(taskID, symbolsDir).Return(nil).Times(1)
	store.EXPECT().UpdateIsFirstBatchStored(taskID).Return(nil).Times(1)

	impl := &p2pImpl{p2p: p2pClient, rqStore: store}
	stored, total, err := impl.storeCascadeSymbolsAndData(context.Background(), taskID, actionID, symbolsDir, nil)
	require.NoError(t, err)
	require.Equal(t, 1, stored)
	require.Equal(t, 1, total)
	require.Equal(t, 1, p2pClient.storeBatchCalls)
	require.Equal(t, P2PDataRaptorQSymbol, p2pClient.lastType)
	require.Equal(t, taskID, p2pClient.lastTaskID)
}
