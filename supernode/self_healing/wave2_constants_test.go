package self_healing

import (
	"testing"
)

// TestMaxReconstructedBytesConstant pins the H7 verifier-side cap so a
// future refactor can't silently zero it. 4 GiB is intentionally large
// enough to accommodate any real cascade action while bounding the
// worst-case verifier RAM footprint.
func TestMaxReconstructedBytesConstant(t *testing.T) {
	const minSane uint64 = 64 * 1024 * 1024 // 64 MiB lower bound
	if MaxReconstructedBytes < minSane {
		t.Fatalf("MaxReconstructedBytes=%d under sane lower bound %d", MaxReconstructedBytes, minSane)
	}
	if MaxReconstructedBytes == 0 {
		t.Fatalf("MaxReconstructedBytes must NOT be zero — that disables the H7 cap")
	}
}

// TestNegativeAttestationTaxonomy pins the L2 canonical reason set so
// adding a new reason category requires touching this test (and ensures
// the constants stay non-empty).
func TestNegativeAttestationTaxonomy(t *testing.T) {
	cases := []struct {
		name, val string
	}{
		{"fetch_failed", negativeReasonFetchFailed},
		{"hash_compute_failed", negativeReasonHashCompute},
		{"hash_mismatch", negativeReasonHashMismatch},
		{"other", negativeReasonOther},
	}
	seen := map[string]struct{}{}
	for _, tc := range cases {
		if tc.val == "" {
			t.Fatalf("%s: empty taxonomy value", tc.name)
		}
		if _, dup := seen[tc.val]; dup {
			t.Fatalf("%s: duplicate taxonomy value %q", tc.name, tc.val)
		}
		seen[tc.val] = struct{}{}
		// All values share the "reason_" prefix for grep-ability.
		if got := negativeAttestationHash(tc.val); got == "" {
			t.Fatalf("%s: empty hash for %q", tc.name, tc.val)
		}
	}
	// Two different reason categories must produce different hashes.
	if negativeAttestationHash(negativeReasonFetchFailed) == negativeAttestationHash(negativeReasonHashMismatch) {
		t.Fatalf("distinct reason categories must produce distinct hashes")
	}
}
