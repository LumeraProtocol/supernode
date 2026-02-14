package adaptors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/storage/rqstore"
	"go.uber.org/mock/gomock"
)

type clientWithPeersCount struct {
	p2p.Client
	peers int
}

func (c clientWithPeersCount) PeersCount() int { return c.peers }

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

