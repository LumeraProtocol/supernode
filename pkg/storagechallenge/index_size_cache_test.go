package storagechallenge

import (
	"fmt"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
)

// TestIndexSizeCache_M4_HitReturnsCachedSize pins LEP-6 review M4 (Matee):
// the second ResolveArtifactSize call for the same INDEX inputs must hit the
// cache (no regenerate). We assert this indirectly by populating the cache
// once and verifying the entry is reachable via globalIndexSizeCache.get with
// the same key.
func TestIndexSizeCache_M4_HitReturnsCachedSize(t *testing.T) {
	act := &actiontypes.Action{FileSizeKbs: 10}
	meta := &actiontypes.CascadeMetadata{Signatures: "index-signature-format", RqIdsIc: 2, RqIdsMax: 5}

	// Cold call → primes the cache.
	first, err := ResolveArtifactSize(act, meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 1)
	if err != nil {
		t.Fatalf("cold ResolveArtifactSize: %v", err)
	}

	key := indexSizeKey(meta.Signatures, uint32(meta.RqIdsIc), uint32(meta.RqIdsMax))
	cached := globalIndexSizeCache.get(key)
	if cached == nil {
		t.Fatalf("M4 regression: cache must have an entry after cold call")
	}
	if int(1) >= len(cached) || cached[1] != first {
		t.Fatalf("M4 cache content mismatch: cached[1]=%d, want %d", cached[1], first)
	}

	// Warm call → returns same value.
	second, err := ResolveArtifactSize(act, meta, audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 1)
	if err != nil {
		t.Fatalf("warm ResolveArtifactSize: %v", err)
	}
	if first != second {
		t.Fatalf("M4 regression: cold=%d warm=%d (must match)", first, second)
	}
}

// TestIndexSizeCache_M4_LRUEvictionBoundedAt256 pins the cache cap so that a
// hot supernode handling thousands of distinct tickets does not unboundedly
// grow heap.
func TestIndexSizeCache_M4_LRUEvictionBoundedAt256(t *testing.T) {
	c := newIndexSizeCache(indexSizeCacheCap)
	for i := 0; i < indexSizeCacheCap*4; i++ {
		key := indexSizeKey(fmt.Sprintf("sig-%d", i), 1, 1)
		c.put(key, []uint64{uint64(i)})
	}
	if got, want := c.length(), indexSizeCacheCap; got != want {
		t.Fatalf("M4 LRU bound violated: length=%d, want=%d", got, want)
	}
}
