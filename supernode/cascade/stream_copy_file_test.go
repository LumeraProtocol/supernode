package cascade

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// TestStreamCopyFile_PreservesContents covers the M5 fix: the streaming
// copy must produce a byte-for-byte identical file (we replaced the
// os.ReadFile + os.WriteFile RAM-blowup pattern with io.Copy).
func TestStreamCopyFile_PreservesContents(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	dst := filepath.Join(tmp, "dst.bin")

	// Generate a non-trivial payload spanning multiple internal buffer
	// flushes (default io.Copy buffer is 32 KiB).
	body := make([]byte, 256*1024) // 256 KiB
	for i := range body {
		body[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := streamCopyFile(src, dst); err != nil {
		t.Fatalf("streamCopyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("copied length %d; want %d", len(got), len(body))
	}
	if sha256.Sum256(got) != sha256.Sum256(body) {
		t.Fatalf("copied bytes differ from src")
	}
	// Permissions match staging convention.
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if got, want := st.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("dst perm = %o; want %o", got, want)
	}
}

// TestStreamCopyFile_SrcMissing covers error propagation: a missing src
// file must surface as an error rather than silently producing an empty
// dst.
func TestStreamCopyFile_SrcMissing(t *testing.T) {
	tmp := t.TempDir()
	if err := streamCopyFile(filepath.Join(tmp, "nope"), filepath.Join(tmp, "out")); err == nil {
		t.Fatalf("expected error when src is missing")
	}
}
