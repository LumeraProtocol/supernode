package self_healing

import (
	"os"
	"path/filepath"
	"testing"
)

// makeStagingDir creates a minimal heal-op staging dir matching the layout
// produced by cascade.stageArtefacts: manifest.json + reconstructed.bin +
// empty symbols/ subdir. Returns the absolute staging dir path.
func makeStagingDir(t *testing.T, root string, opID uint64, hashB64 string, body []byte) string {
	t.Helper()
	dir := filepath.Join(root, itoa(opID))
	if err := os.MkdirAll(filepath.Join(dir, "symbols"), 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reconstructed.bin"), body, 0o600); err != nil {
		t.Fatalf("write reconstructed: %v", err)
	}
	manifest := []byte(`{"action_id":"ticket-` + itoa(opID) + `","layout":{"blocks":[]},"id_files":[],"symbol_keys":[],"symbols_dir":"` + filepath.Join(dir, "symbols") + `","reconstructed_rel":"reconstructed.bin","manifest_hash_b64":"` + hashB64 + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
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
