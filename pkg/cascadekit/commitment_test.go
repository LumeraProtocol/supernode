package cascadekit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "file.bin")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

func TestVerifyCommitmentRoot_Valid(t *testing.T) {
	path := writeTempFile(t, []byte("hello lep5 commitment"))
	commitment, _, err := BuildCommitmentFromFile(path, 8, 4)
	if err != nil {
		t.Fatalf("build commitment: %v", err)
	}

	if _, err := VerifyCommitmentRoot(path, commitment); err != nil {
		t.Fatalf("verify commitment root: %v", err)
	}
}

func TestVerifyCommitmentRoot_RejectsInvalidRootLength(t *testing.T) {
	path := writeTempFile(t, []byte("hello lep5 commitment"))
	commitment, _, err := BuildCommitmentFromFile(path, 8, 4)
	if err != nil {
		t.Fatalf("build commitment: %v", err)
	}
	commitment.Root = []byte{1, 2, 3}

	_, err = VerifyCommitmentRoot(path, commitment)
	if err == nil || !strings.Contains(err.Error(), "invalid root length") {
		t.Fatalf("expected invalid root length error, got: %v", err)
	}
}

func TestVerifyCommitmentRoot_RejectsInvalidChunkSize(t *testing.T) {
	path := writeTempFile(t, []byte("hello lep5 commitment"))
	commitment, _, err := BuildCommitmentFromFile(path, 8, 4)
	if err != nil {
		t.Fatalf("build commitment: %v", err)
	}
	commitment.ChunkSize = 0

	_, err = VerifyCommitmentRoot(path, commitment)
	if err == nil || !strings.Contains(err.Error(), "invalid chunk size") {
		t.Fatalf("expected invalid chunk size error, got: %v", err)
	}
}
