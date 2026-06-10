package cascade

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/supernode/adaptors"
)

// fakeStoreP2P scripts StoreArtefacts to fail its first failN calls with errFn,
// then succeed. It records the IdempotentDirectoryRecord flag seen per call.
type fakeStoreP2P struct {
	mu            sync.Mutex
	calls         int
	failN         int
	err           error
	gotIdempotent []bool
}

func (f *fakeStoreP2P) StoreArtefacts(_ context.Context, req adaptors.StoreArtefactsRequest, _ logtrace.Fields) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotIdempotent = append(f.gotIdempotent, req.IdempotentDirectoryRecord)
	if f.calls <= f.failN {
		return f.err
	}
	return nil
}

func newStoreTask(p adaptors.P2PService) *CascadeRegistrationTask {
	return &CascadeRegistrationTask{
		CascadeService: &CascadeService{P2P: p},
		taskID:         "task-test",
	}
}

func withFastStoreBackoff(t *testing.T) {
	t.Helper()
	orig := storeArtefactsRetryBackoff
	storeArtefactsRetryBackoff = time.Millisecond
	t.Cleanup(func() { storeArtefactsRetryBackoff = orig })
}

func TestStoreArtefacts_RetriesTransientThenSucceeds(t *testing.T) {
	withFastStoreBackoff(t)
	p := &fakeStoreP2P{
		failN: 2,
		err:   errors.New("error storing artefacts: p2p store batch (first): iterate batch store: no eligible store peers for 100/100 keys"),
	}
	task := newStoreTask(p)

	err := task.storeArtefacts(context.Background(), "action-1", [][]byte{{1}}, "/tmp/symbols", codec.Layout{}, nil)
	if err != nil {
		t.Fatalf("expected success after transient retries, got: %v", err)
	}
	if p.calls != 3 {
		t.Fatalf("expected 3 attempts (2 transient + 1 success), got %d", p.calls)
	}
	// First attempt uses the non-idempotent path; retries must force the
	// idempotent directory upsert so the re-inserted symbol-dir row doesn't fail.
	if p.gotIdempotent[0] {
		t.Errorf("first attempt should not force IdempotentDirectoryRecord")
	}
	if !p.gotIdempotent[1] || !p.gotIdempotent[2] {
		t.Errorf("retry attempts must force IdempotentDirectoryRecord, got %v", p.gotIdempotent)
	}
}

func TestStoreArtefacts_DoesNotRetryNonTransient(t *testing.T) {
	withFastStoreBackoff(t)
	p := &fakeStoreP2P{
		failN: 99,
		err:   errors.New("error storing artefacts: store symbol dir: bad layout"),
	}
	task := newStoreTask(p)

	err := task.storeArtefacts(context.Background(), "action-1", [][]byte{{1}}, "/tmp/symbols", codec.Layout{}, nil)
	if err == nil {
		t.Fatal("expected error for non-transient failure")
	}
	if p.calls != 1 {
		t.Fatalf("non-transient error must not be retried, got %d attempts", p.calls)
	}
}

func TestStoreArtefacts_GivesUpAfterMaxAttempts(t *testing.T) {
	withFastStoreBackoff(t)
	p := &fakeStoreP2P{
		failN: 99,
		err:   errors.New("error storing artefacts: p2p store batch (first): iterate batch store: failed to achieve desired success rate, only: 0.00% successful"),
	}
	task := newStoreTask(p)

	err := task.storeArtefacts(context.Background(), "action-1", [][]byte{{1}}, "/tmp/symbols", codec.Layout{}, nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if p.calls != storeArtefactsMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", storeArtefactsMaxAttempts, p.calls)
	}
}
