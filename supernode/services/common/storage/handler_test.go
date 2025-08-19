package storage

import (
	"context"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/p2p/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type mockP2PClient struct {
	mocks.Client
}

type mockStore struct {
	mock.Mock
}

func (m *mockStore) StoreSymbolDirectory(taskID, dir string) error {
	args := m.Called(taskID, dir)
	return args.Error(0)
}

func (m *mockStore) UpdateIsFirstBatchStored(txID string) error {
	args := m.Called(txID)
	return args.Error(0)
}

func TestStoreBytesIntoP2P(t *testing.T) {
	p2pClient := new(mockP2PClient)
	handler := NewStorageHandler(p2pClient, "", nil)

	data := []byte("hello")
	p2pClient.On("Store", mock.Anything, data, 1).Return("some-id", nil)

	id, err := handler.StoreBytesIntoP2P(context.Background(), data, 1)
	assert.NoError(t, err)
	assert.Equal(t, "some-id", id)
	p2pClient.AssertExpectations(t)
}

func TestStoreBatch(t *testing.T) {
	p2pClient := new(mockP2PClient)
	handler := NewStorageHandler(p2pClient, "", nil)

	ctx := context.WithValue(context.Background(), "task_id", "123")
	list := [][]byte{[]byte("a"), []byte("b")}
	p2pClient.On("StoreBatch", mock.Anything, list, 3, "").Return(nil)

	err := handler.StoreBatch(ctx, list, 3)
	assert.NoError(t, err)
}
