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

func TestDeriveSimpleIndices_DeterministicAndUnique(t *testing.T) {
	root := []byte("fixed-root-seed")
	gotA := deriveSimpleIndices(root, 16, 8)
	gotB := deriveSimpleIndices(root, 16, 8)

	if len(gotA) != 8 || len(gotB) != 8 {
		t.Fatalf("unexpected lengths: %d, %d", len(gotA), len(gotB))
	}

	for i := range gotA {
		if gotA[i] != gotB[i] {
			t.Fatalf("non-deterministic output at %d: %d != %d", i, gotA[i], gotB[i])
		}
	}

	seen := make(map[uint32]struct{}, len(gotA))
	for _, idx := range gotA {
		if idx >= 16 {
			t.Fatalf("index out of range: %d", idx)
		}
		if _, ok := seen[idx]; ok {
			t.Fatalf("duplicate index: %d", idx)
		}
		seen[idx] = struct{}{}
	}
}

func TestDeriveSimpleIndices_CoversAllWhenMEqualsNumChunks(t *testing.T) {
	root := []byte("another-fixed-root-seed")
	numChunks := uint32(7)
	got := deriveSimpleIndices(root, numChunks, numChunks)
	if len(got) != int(numChunks) {
		t.Fatalf("expected %d indices, got %d", numChunks, len(got))
	}

	seen := make(map[uint32]struct{}, len(got))
	for _, idx := range got {
		if idx >= numChunks {
			t.Fatalf("index out of range: %d", idx)
		}
		seen[idx] = struct{}{}
	}
	if len(seen) != int(numChunks) {
		t.Fatalf("expected full coverage of [0,%d), got %d unique indices", numChunks, len(seen))
	}
}
