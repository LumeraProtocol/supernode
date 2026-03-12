package adaptors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	p2pmock "github.com/LumeraProtocol/supernode/v2/p2p/mocks"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"github.com/stretchr/testify/mock"
	"go.uber.org/mock/gomock"
)

type clientWithPeersCount struct {
	p2p.Client
	peers int
}

func (c clientWithPeersCount) PeersCount() int { return c.peers }

type p2pClientWithStreamMock struct {
	*p2pmock.Client
}

func (c p2pClientWithStreamMock) BatchRetrieveStream(_ context.Context, _ []string, _ int32, _ string, _ func(string, []byte) error, _ ...bool) (int32, error) {
	return 0, nil
}

func TestStoreArtefacts_ZeroPeers_ReturnsError(t *testing.T) {
	svc := NewP2PService(clientWithPeersCount{peers: 0}, nil)

	err := svc.StoreArtefacts(context.Background(), StoreArtefactsRequest{TaskID: "task"}, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "zero peers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStoreArtefacts_PeersPresent_DoesNotTripGuard(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	store := rqstore.NewMockStore(ctrl)
	storeErr := errors.New("store down")
	store.EXPECT().StoreSymbolDirectory("task", "").Return(storeErr)

	svc := NewP2PService(clientWithPeersCount{peers: 1}, store)

	err := svc.StoreArtefacts(context.Background(), StoreArtefactsRequest{TaskID: "task"}, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if strings.Contains(err.Error(), "zero peers") {
		t.Fatalf("guard should not have fired, got: %v", err)
	}
	if !strings.Contains(err.Error(), storeErr.Error()) {
		t.Fatalf("expected wrapped store error, got: %v", err)
	}
}

func TestStoreCascadeSymbolsAndData_MetadataOnlyBatchWhenNoSymbols(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	store := rqstore.NewMockStore(ctrl)
	store.EXPECT().StoreSymbolDirectory("task", "").Return(nil)
	store.EXPECT().UpdateIsFirstBatchStored("task").Return(nil)

	metadata := [][]byte{[]byte("index-bytes"), []byte("layout-bytes")}
	baseClient := p2pmock.NewClient(t)
	baseClient.On("StoreBatch", mock.Anything, metadata, P2PDataRaptorQSymbol, "task").Return(nil).Once()

	svc := &p2pImpl{p2p: p2pClientWithStreamMock{Client: baseClient}, rqStore: store}
	stored, total, err := svc.storeCascadeSymbolsAndData(context.Background(), "task", "action", "", metadata, codec.Layout{Blocks: []codec.Block{{BlockID: 0}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stored != 0 {
		t.Fatalf("expected 0 stored symbols, got %d", stored)
	}
	if total != 0 {
		t.Fatalf("expected 0 total symbols, got %d", total)
	}
}

func TestStoreCascadeSymbolsAndData_MetadataOnlyBatchFailureSkipsFirstBatchFlag(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	store := rqstore.NewMockStore(ctrl)
	store.EXPECT().StoreSymbolDirectory("task", "").Return(nil)
	store.EXPECT().UpdateIsFirstBatchStored("task").Times(0)

	metadata := [][]byte{[]byte("index-bytes")}
	baseClient := p2pmock.NewClient(t)
	baseClient.On("StoreBatch", mock.Anything, metadata, P2PDataRaptorQSymbol, "task").Return(errors.New("p2p down")).Once()

	svc := &p2pImpl{p2p: p2pClientWithStreamMock{Client: baseClient}, rqStore: store}
	_, _, err := svc.storeCascadeSymbolsAndData(context.Background(), "task", "action", "", metadata, codec.Layout{Blocks: []codec.Block{{BlockID: 0}}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "metadata-only") {
		t.Fatalf("expected metadata-only path error, got: %v", err)
	}
}

