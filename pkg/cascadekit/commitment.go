package cascadekit

import (
	"fmt"
	"io"
	"os"

	"github.com/LumeraProtocol/lumera/x/action/v1/merkle"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"lukechampine.com/blake3"
)

const (
	// DefaultChunkSize is the default chunk size for LEP-5 commitment (256 KiB).
	DefaultChunkSize = 262144
	// MinChunkSize is the minimum allowed chunk size.
	MinChunkSize = 1
	// MaxChunkSize is the maximum allowed chunk size.
	MaxChunkSize = 262144
	// MinTotalSize is the minimum file size for LEP-5 commitment.
	MinTotalSize = 4
	// CommitmentType is the commitment type constant for LEP-5.
	CommitmentType = "lep5/chunk-merkle/v1"
)

// SelectChunkSize returns the optimal chunk size for a given file size and
// minimum chunk count. It starts at DefaultChunkSize and halves until the
// file produces at least minChunks chunks.
func SelectChunkSize(fileSize int64, minChunks uint32) uint32 {
	s := uint32(DefaultChunkSize)
	for numChunks(fileSize, s) < minChunks && s > MinChunkSize {
		s /= 2
	}
	return s
}

func numChunks(fileSize int64, chunkSize uint32) uint32 {
	n := uint32(fileSize / int64(chunkSize))
	if fileSize%int64(chunkSize) != 0 {
		n++
	}
	return n
}

// ChunkFile reads a file and returns its chunks using the given chunk size.
func ChunkFile(path string, chunkSize uint32) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	totalSize := fi.Size()
	n := numChunks(totalSize, chunkSize)
	chunks := make([][]byte, 0, n)

	buf := make([]byte, chunkSize)
	for {
		nr, err := io.ReadFull(f, buf)
		if nr > 0 {
			chunk := make([]byte, nr)
			copy(chunk, buf[:nr])
			chunks = append(chunks, chunk)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read chunk: %w", err)
		}
	}
	return chunks, nil
}

// BuildCommitmentFromFile constructs an AvailabilityCommitment for a file.
// It chunks the file, builds a Merkle tree, and generates challenge indices.
// challengeCount and minChunks are the SVC parameters from the chain.
func BuildCommitmentFromFile(filePath string, challengeCount, minChunks uint32) (*actiontypes.AvailabilityCommitment, *merkle.Tree, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}
	totalSize := fi.Size()
	if totalSize < MinTotalSize {
		return nil, nil, fmt.Errorf("file too small: %d bytes (minimum %d)", totalSize, MinTotalSize)
	}

	chunkSize := SelectChunkSize(totalSize, minChunks)
	nc := numChunks(totalSize, chunkSize)
	if nc < minChunks {
		return nil, nil, fmt.Errorf("file produces %d chunks, need at least %d", nc, minChunks)
	}

	chunks, err := ChunkFile(filePath, chunkSize)
	if err != nil {
		return nil, nil, err
	}

	tree, err := merkle.BuildTree(chunks)
	if err != nil {
		return nil, nil, fmt.Errorf("build merkle tree: %w", err)
	}

	// Generate challenge indices — simple deterministic selection using tree root as entropy.
	m := challengeCount
	if m > nc {
		m = nc
	}
	indices := deriveSimpleIndices(tree.Root[:], nc, m)

	commitment := &actiontypes.AvailabilityCommitment{
		CommitmentType:   CommitmentType,
		HashAlgo:         actiontypes.HashAlgo_HASH_ALGO_BLAKE3,
		ChunkSize:        chunkSize,
		TotalSize:        uint64(totalSize),
		NumChunks:        nc,
		Root:             tree.Root[:],
		ChallengeIndices: indices,
	}

	return commitment, tree, nil
}

// GenerateChunkProofs produces Merkle proofs for the challenge indices in the commitment.
func GenerateChunkProofs(tree *merkle.Tree, indices []uint32) ([]*actiontypes.ChunkProof, error) {
	proofs := make([]*actiontypes.ChunkProof, len(indices))
	for i, idx := range indices {
		p, err := tree.GenerateProof(int(idx))
		if err != nil {
			return nil, fmt.Errorf("generate proof for chunk %d: %w", idx, err)
		}

		pathHashes := make([][]byte, len(p.PathHashes))
		for j, h := range p.PathHashes {
			pathHashes[j] = h[:]
		}

		proofs[i] = &actiontypes.ChunkProof{
			ChunkIndex:     p.ChunkIndex,
			LeafHash:       p.LeafHash[:],
			PathHashes:     pathHashes,
			PathDirections: p.PathDirections,
		}
	}
	return proofs, nil
}

// VerifyCommitmentRoot rebuilds the Merkle tree from a file and checks it matches the on-chain root.
func VerifyCommitmentRoot(filePath string, commitment *actiontypes.AvailabilityCommitment) (*merkle.Tree, error) {
	if commitment == nil {
		return nil, nil // pre-LEP-5 action, nothing to verify
	}
	if commitment.ChunkSize < MinChunkSize || commitment.ChunkSize > MaxChunkSize {
		return nil, fmt.Errorf("invalid chunk size in commitment: %d", commitment.ChunkSize)
	}
	if commitment.NumChunks == 0 {
		return nil, fmt.Errorf("invalid num_chunks in commitment: %d", commitment.NumChunks)
	}
	if len(commitment.Root) != merkle.HashSize {
		return nil, fmt.Errorf("invalid root length in commitment: got %d, expected %d", len(commitment.Root), merkle.HashSize)
	}

	chunks, err := ChunkFile(filePath, commitment.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("chunk file for verification: %w", err)
	}

	if uint32(len(chunks)) != commitment.NumChunks {
		return nil, fmt.Errorf("chunk count mismatch: got %d, expected %d", len(chunks), commitment.NumChunks)
	}

	tree, err := merkle.BuildTree(chunks)
	if err != nil {
		return nil, fmt.Errorf("build merkle tree for verification: %w", err)
	}

	var expectedRoot [merkle.HashSize]byte
	copy(expectedRoot[:], commitment.Root)
	if tree.Root != expectedRoot {
		return nil, fmt.Errorf("merkle root mismatch: computed %x, expected %x", tree.Root[:], commitment.Root)
	}

	return tree, nil
}

// deriveSimpleIndices generates m unique indices in [0, numChunks) using BLAKE3(root || counter).
func deriveSimpleIndices(root []byte, numChunks, m uint32) []uint32 {
	if numChunks == 0 || m == 0 {
		return nil
	}

	indices := make([]uint32, 0, m)
	used := make(map[uint32]struct{}, m)
	counter := uint32(0)

	// Allocate once and only overwrite the counter bytes per iteration.
	buf := make([]byte, len(root)+4)
	copy(buf, root)

	// Guard against pathological runtimes from pure rejection sampling when m ~= numChunks.
	maxAttempts := numChunks * 32
	if maxAttempts < m {
		maxAttempts = m
	}

	for uint32(len(indices)) < m && counter < maxAttempts {
		// BLAKE3(root || uint32be(counter))
		buf[len(root)] = byte(counter >> 24)
		buf[len(root)+1] = byte(counter >> 16)
		buf[len(root)+2] = byte(counter >> 8)
		buf[len(root)+3] = byte(counter)

		h := blake3.Sum256(buf)
		// Use first 8 bytes as uint64 mod numChunks
		val := uint64(h[0])<<56 | uint64(h[1])<<48 | uint64(h[2])<<40 | uint64(h[3])<<32 |
			uint64(h[4])<<24 | uint64(h[5])<<16 | uint64(h[6])<<8 | uint64(h[7])
		idx := uint32(val % uint64(numChunks))

		if _, exists := used[idx]; !exists {
			used[idx] = struct{}{}
			indices = append(indices, idx)
		}
		counter++
	}

	// Deterministic fallback: fill any missing indices in ascending order.
	for idx := uint32(0); uint32(len(indices)) < m && idx < numChunks; idx++ {
		if _, exists := used[idx]; exists {
			continue
		}
		used[idx] = struct{}{}
		indices = append(indices, idx)
	}

	return indices
}
