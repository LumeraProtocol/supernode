package kademlia

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia/domain"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// fakeStore is a minimal in-memory Store focused on replication_info
// operations used by pruneIneligibleStorePeers. All other methods are no-ops.
type fakeStore struct {
	mu   sync.Mutex
	reps map[string]*domain.NodeReplicationInfo
}

func newFakeStore() *fakeStore { return &fakeStore{reps: map[string]*domain.NodeReplicationInfo{}} }

func (f *fakeStore) Store(ctx context.Context, key, data []byte, typ int, isOriginal bool) error {
	return nil
}
func (f *fakeStore) Retrieve(ctx context.Context, key []byte) ([]byte, error) { return nil, nil }
func (f *fakeStore) Delete(ctx context.Context, key []byte)                   {}
func (f *fakeStore) GetKeysForReplication(ctx context.Context, from, to time.Time, maxKeys int) domain.KeysWithTimestamp {
	return nil
}
func (f *fakeStore) Stats(ctx context.Context) (DatabaseStats, error) { return DatabaseStats{}, nil }
func (f *fakeStore) Close(ctx context.Context)                        {}
func (f *fakeStore) Count(ctx context.Context) (int, error)           { return 0, nil }
func (f *fakeStore) DeleteAll(ctx context.Context) error              { return nil }
func (f *fakeStore) UpdateKeyReplication(ctx context.Context, key []byte) error {
	return nil
}
func (f *fakeStore) StoreBatch(ctx context.Context, values [][]byte, typ int, isOriginal bool) error {
	return nil
}
func (f *fakeStore) GetAllReplicationInfo(ctx context.Context) ([]domain.NodeReplicationInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.NodeReplicationInfo, 0, len(f.reps))
	for _, r := range f.reps {
		out = append(out, *r)
	}
	return out, nil
}
func (f *fakeStore) UpdateReplicationInfo(ctx context.Context, rep domain.NodeReplicationInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := rep
	f.reps[string(rep.ID)] = &cp
	return nil
}
func (f *fakeStore) AddReplicationInfo(ctx context.Context, rep domain.NodeReplicationInfo) error {
	return f.UpdateReplicationInfo(ctx, rep)
}
func (f *fakeStore) GetOwnCreatedAt(ctx context.Context) (time.Time, error) {
	return time.Time{}, nil
}
func (f *fakeStore) StoreBatchRepKeys(values []string, id, ip string, port uint16) error { return nil }
func (f *fakeStore) GetAllToDoRepKeys(minA, maxA int) (domain.ToRepKeys, error)           { return nil, nil }
func (f *fakeStore) DeleteRepKey(key string) error                                        { return nil }
func (f *fakeStore) UpdateLastSeen(ctx context.Context, id string) error                  { return nil }
func (f *fakeStore) RetrieveBatchNotExist(ctx context.Context, keys []string, batchSize int) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) RetrieveBatchValues(ctx context.Context, keys []string, getFromCloud bool) ([][]byte, int, error) {
	return nil, 0, nil
}
func (f *fakeStore) BatchDeleteRepKeys(keys []string) error { return nil }
func (f *fakeStore) IncrementAttempts(keys []string) error  { return nil }
func (f *fakeStore) UpdateIsActive(ctx context.Context, id string, isActive, isAdjusted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.reps[id]; ok {
		r.Active = isActive
		r.IsAdjusted = isAdjusted
	}
	return nil
}
func (f *fakeStore) UpdateIsAdjusted(ctx context.Context, id string, isAdjusted bool) error {
	return nil
}
func (f *fakeStore) UpdateLastReplicated(ctx context.Context, id string, t time.Time) error {
	return nil
}
func (f *fakeStore) RecordExists(nodeID string) (bool, error)                { return false, nil }
func (f *fakeStore) GetLocalKeys(from, to time.Time) ([]string, error)       { return nil, nil }
func (f *fakeStore) BatchDeleteRecords(keys []string) error                  { return nil }

// compile-time check
var _ Store = (*fakeStore)(nil)

// I5: eager prune flips replication_info.Active=false for peers that are
// routing-eligible (e.g. STORAGE_FULL) but not store-eligible.
func TestPruneIneligibleStorePeers_ClearsNonStorePeers(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()

	activeID := []byte("active-peer-id")
	storageFullID := []byte("storage-full-peer-id")
	now := time.Now()
	for _, id := range [][]byte{activeID, storageFullID} {
		_ = store.AddReplicationInfo(ctx, domain.NodeReplicationInfo{
			ID: id, IP: "127.0.0.1", Port: 4445, Active: true,
			UpdatedAt: now, CreatedAt: now,
		})
	}

	activeHash, _ := utils.Blake3Hash(activeID)
	var ak [32]byte
	copy(ak[:], activeHash)

	d := &DHT{store: store}
	d.setStoreAllowlist(ctx, map[[32]byte]struct{}{ak: {}})

	d.pruneIneligibleStorePeers(ctx)

	infos, _ := store.GetAllReplicationInfo(ctx)
	byID := map[string]domain.NodeReplicationInfo{}
	for _, i := range infos {
		byID[string(i.ID)] = i
	}
	if got := byID[string(activeID)]; !got.Active {
		t.Errorf("ACTIVE peer: Active=false; want true")
	}
	if got := byID[string(storageFullID)]; got.Active {
		t.Errorf("STORAGE_FULL peer: Active=true; want false (eagerly pruned)")
	}
}

// I5 boundary: not-ready store allowlist must not flip anything.
func TestPruneIneligibleStorePeers_SkipsWhenNotReady(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	id := []byte("some-peer")
	now := time.Now()
	_ = store.AddReplicationInfo(ctx, domain.NodeReplicationInfo{
		ID: id, IP: "127.0.0.1", Port: 4445, Active: true,
		UpdatedAt: now, CreatedAt: now,
	})

	d := &DHT{store: store}
	// No setStoreAllowlist => not ready.
	d.pruneIneligibleStorePeers(ctx)

	infos, _ := store.GetAllReplicationInfo(ctx)
	if len(infos) != 1 || !infos[0].Active {
		t.Fatalf("expected no change when not-ready; got %+v", infos)
	}
}
