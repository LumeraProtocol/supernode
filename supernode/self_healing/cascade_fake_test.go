package self_healing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

// fakeCascadeFactory.NewCascadeRegistrationTask returns a programmable
// fakeCascadeTask. The healer flow exercises only RecoveryReseed; the
// finalizer only PublishStagedArtefacts. Other methods panic — a regression
// that calls Register/Download in the heal path is loud.
type fakeCascadeFactory struct {
	mu               sync.Mutex
	reseedFn         func(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error)
	publishFn        func(ctx context.Context, stagingDir string) error
	publishCalls     atomic.Int64
	reseedCalls      atomic.Int64
	lastPublishedDir atomic.Value // string
}

func newFakeCascadeFactory() *fakeCascadeFactory {
	f := &fakeCascadeFactory{}
	f.lastPublishedDir.Store("")
	return f
}

func (f *fakeCascadeFactory) NewCascadeRegistrationTask() cascadeService.CascadeTask {
	return &fakeCascadeTask{f: f}
}

type fakeCascadeTask struct {
	f *fakeCascadeFactory
}

func (t *fakeCascadeTask) Register(ctx context.Context, req *cascadeService.RegisterRequest, send func(resp *cascadeService.RegisterResponse) error) error {
	panic("self_healing test: cascade Register must not be called")
}
func (t *fakeCascadeTask) Download(ctx context.Context, req *cascadeService.DownloadRequest, send func(resp *cascadeService.DownloadResponse) error) error {
	panic("self_healing test: cascade Download must not be called")
}
func (t *fakeCascadeTask) CleanupDownload(ctx context.Context, tmpDir string) error { return nil }

func (t *fakeCascadeTask) RecoveryReseed(ctx context.Context, req *cascadeService.RecoveryReseedRequest) (*cascadeService.RecoveryReseedResult, error) {
	t.f.reseedCalls.Add(1)
	t.f.mu.Lock()
	fn := t.f.reseedFn
	t.f.mu.Unlock()
	if fn == nil {
		return nil, errors.New("fakeCascade: no reseedFn configured")
	}
	return fn(ctx, req)
}

func (t *fakeCascadeTask) PublishStagedArtefacts(ctx context.Context, stagingDir string) error {
	t.f.publishCalls.Add(1)
	t.f.lastPublishedDir.Store(stagingDir)
	t.f.mu.Lock()
	fn := t.f.publishFn
	t.f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, stagingDir)
}

// makeStagingDir creates an empty staging dir + minimal manifest+reconstructed
// file pair the §19 transport expects. Useful for finalizer tests that don't
// drive the full RecoveryReseed.
func makeStagingDir(t testing_T, root string, opID uint64, hashB64 string, body []byte) string {
	dir := filepath.Join(root, itoa(opID))
	mustMkdir(t, dir)
	mustMkdir(t, filepath.Join(dir, "symbols"))
	mustWrite(t, filepath.Join(dir, "reconstructed.bin"), body)
	manifest := []byte(`{"action_id":"ticket-` + itoa(opID) + `","layout":{"blocks":[]},"id_files":[],"symbol_keys":[],"symbols_dir":"` + filepath.Join(dir, "symbols") + `","reconstructed_rel":"reconstructed.bin","manifest_hash_b64":"` + hashB64 + `"}`)
	mustWrite(t, filepath.Join(dir, "manifest.json"), manifest)
	return dir
}

// minimal testing.T-like surface so test helpers can be reused without
// importing testing.B.
type testing_T interface {
	Helper()
	Fatalf(format string, args ...interface{})
}

func mustMkdir(t testing_T, p string) {
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Helper()
		t.Fatalf("mkdir %q: %v", p, err)
	}
}
func mustWrite(t testing_T, p string, b []byte) {
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Helper()
		t.Fatalf("write %q: %v", p, err)
	}
}
func itoa(u uint64) string {
	if u == 0 {
		return "0"
	}
	digits := []byte{}
	for u > 0 {
		digits = append([]byte{byte('0' + u%10)}, digits...)
		u /= 10
	}
	return string(digits)
}
