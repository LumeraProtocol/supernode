package deterministic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"lukechampine.com/blake3"
)

// chainSeed reproduces the test fixture used by the chain's
// audit_peer_assignment_test.go::TestStorageTruthAssignmentUsesOneThirdCoverage.
// Keeping it identical here lets us cross-check supernode ↔ chain behaviour
// against the same input.
var chainSeed = []byte("01234567890123456789012345678901")

func TestStorageTruthAssignmentHash_KnownVector(t *testing.T) {
	// Byte-level expectation locked against an independent SHA-256
	// computation of the chain's exact byte composition:
	//   seed || 0x00 || "sn-a" || 0x00 || "challenge_target"
	got := storageTruthAssignmentHash(chainSeed, "sn-a", "challenge_target")
	wantHex := "bf2bd1e684b3640d2bb047f4d71db719f8f4aa2c3b1601df492115f6e3552b7f"
	want, _ := hex.DecodeString(wantHex)
	if !bytes.Equal(got, want) {
		t.Fatalf("storageTruthAssignmentHash mismatch\nwant %s\ngot  %s", wantHex, hex.EncodeToString(got))
	}

	// Spot-check the helper interleaves NULs correctly — direct SHA-256 of
	// the equivalent byte stream must match.
	h := sha256.New()
	h.Write(chainSeed)
	h.Write([]byte{0})
	h.Write([]byte("sn-a"))
	h.Write([]byte{0})
	h.Write([]byte("challenge_target"))
	if !bytes.Equal(got, h.Sum(nil)) {
		t.Fatalf("storageTruthAssignmentHash diverges from inline SHA-256")
	}
}

func TestSelectLEP6Targets_OneThirdCoverage_AssignmentMatchesChain(t *testing.T) {
	active := []string{"sn-a", "sn-b", "sn-c", "sn-d", "sn-e", "sn-f"}
	targets := SelectLEP6Targets(active, chainSeed, 3)
	// Chain test asserts targetCount == 2 with len(active)=6, divisor=3.
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d (%v)", len(targets), targets)
	}
	// Independently computed below in a Python sketch (see PR description);
	// freezing here as a regression vector.
	want := []string{"sn-f", "sn-e"}
	if !equalSliceOrdered(targets, want) {
		t.Fatalf("targets mismatch\nwant %v\ngot  %v", want, targets)
	}
}

func TestAssignChallengerTargets_KnownAssignment(t *testing.T) {
	active := []string{"sn-a", "sn-b", "sn-c", "sn-d", "sn-e", "sn-f"}
	targets := SelectLEP6Targets(active, chainSeed, 3)
	got := AssignChallengerTargets(active, targets, chainSeed)
	want := map[string]string{"sn-a": "sn-f", "sn-b": "sn-e"}
	if len(got) != len(want) {
		t.Fatalf("assignment size mismatch\nwant %v\ngot  %v", want, got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("assignment[%s] = %s, want %s (full got=%v)", k, got[k], v, got)
		}
	}
	// Self-assignment must never happen.
	for c, tg := range got {
		if c == tg {
			t.Fatalf("challenger %s was assigned to itself", c)
		}
	}
	// All assigned targets must come from the SelectLEP6Targets set.
	allowed := map[string]struct{}{}
	for _, x := range targets {
		allowed[x] = struct{}{}
	}
	for _, tg := range got {
		if _, ok := allowed[tg]; !ok {
			t.Fatalf("assigned target %s not in target set %v", tg, targets)
		}
	}
	// No two challengers share the same target.
	seen := map[string]string{}
	for c, tg := range got {
		if prev, dup := seen[tg]; dup {
			t.Fatalf("target %s assigned to both %s and %s", tg, prev, c)
		}
		seen[tg] = c
	}
}

func TestAssignChallengerTargets_SelfSelectedTargetFallsBackLikeChain(t *testing.T) {
	active := []string{"sn-a", "sn-b"}
	// Force the final Lumera edge case: only selected target is the first
	// challenger itself. Chain audit_peer_assignment.go then falls back to the
	// full ranked candidate set and assigns the best non-self candidate instead
	// of returning no assignment.
	got := AssignChallengerTargetsWithCandidates(active, []string{"sn-a"}, active, chainSeed)
	if got["sn-a"] != "sn-b" {
		t.Fatalf("expected self-target fallback sn-a→sn-b, got %v", got)
	}
	if _, assignedSecond := got["sn-b"]; assignedSecond {
		t.Fatalf("targetCount=1 should stop after one assignment, got %v", got)
	}
}

func TestAssignChallengerTargets_DeduplicatesSelectedTargetsForStopCondition(t *testing.T) {
	active := []string{"sn-a", "sn-b", "sn-c"}
	got := AssignChallengerTargetsWithCandidates(active, []string{"sn-a", "sn-a", ""}, active, chainSeed)
	if len(got) != 1 {
		t.Fatalf("duplicate/empty selected targets should count as one unique target, got %v", got)
	}
	for challenger, target := range got {
		if challenger == target {
			t.Fatalf("challenger %s was assigned to itself", challenger)
		}
	}
}

func TestSelectLEP6Targets_SmallSets(t *testing.T) {
	// targetCount must always be ≥1 even when divisor > activeCount.
	got := SelectLEP6Targets([]string{"sn-a", "sn-b"}, chainSeed, 3)
	if len(got) != 1 {
		t.Fatalf("targetCount should clamp to 1, got %d (%v)", len(got), got)
	}
	got = SelectLEP6Targets([]string{"sn-a"}, chainSeed, 3)
	if len(got) != 1 || got[0] != "sn-a" {
		t.Fatalf("singleton should pass through, got %v", got)
	}
	got = SelectLEP6Targets(nil, chainSeed, 3)
	if got != nil {
		t.Fatalf("nil input should yield nil, got %v", got)
	}
	// Divisor zero defaults to LEP6ChallengeTargetDivisor.
	a := SelectLEP6Targets([]string{"sn-a", "sn-b", "sn-c"}, chainSeed, 0)
	b := SelectLEP6Targets([]string{"sn-a", "sn-b", "sn-c"}, chainSeed, LEP6ChallengeTargetDivisor)
	if !equalSliceOrdered(a, b) {
		t.Fatalf("divisor=0 should default to %d; %v != %v", LEP6ChallengeTargetDivisor, a, b)
	}
}

func TestSelectLEP6Targets_DeterministicAcrossRuns(t *testing.T) {
	active := []string{"x", "y", "z", "a", "b", "c", "d", "e", "f", "g", "h"}
	first := SelectLEP6Targets(active, chainSeed, 4)
	for i := 0; i < 50; i++ {
		got := SelectLEP6Targets(active, chainSeed, 4)
		if !equalSliceOrdered(first, got) {
			t.Fatalf("non-deterministic on run %d: %v != %v", i, first, got)
		}
	}
}

func TestPairChallengerToTarget_NoSelfTarget(t *testing.T) {
	got := PairChallengerToTarget("sn-a", []string{"sn-a", "sn-b", "sn-c"}, chainSeed, nil)
	if got == "sn-a" {
		t.Fatalf("PairChallengerToTarget must not return self; got %s", got)
	}
	if got != "sn-b" && got != "sn-c" {
		t.Fatalf("unexpected target %s", got)
	}
}

func TestPairChallengerToTarget_RespectsAssigned(t *testing.T) {
	all := []string{"sn-b", "sn-c", "sn-d"}
	assigned := map[string]struct{}{"sn-b": {}, "sn-c": {}}
	got := PairChallengerToTarget("sn-a", all, chainSeed, assigned)
	if got != "sn-d" {
		t.Fatalf("expected sn-d (only unassigned), got %s", got)
	}
	// All taken → empty.
	full := map[string]struct{}{"sn-b": {}, "sn-c": {}, "sn-d": {}}
	got = PairChallengerToTarget("sn-a", all, chainSeed, full)
	if got != "" {
		t.Fatalf("expected empty when all targets taken, got %s", got)
	}
}

func TestClassifyTicketBucket_Boundaries(t *testing.T) {
	// recent ≤ 3*epoch, old ≥ 30*epoch — using 400-block epochs.
	const epoch = 400
	recent := uint64(3 * epoch) // 1200
	old := uint64(30 * epoch)   // 12000
	cases := []struct {
		name   string
		anchor int64
		now    int64
		want   audittypes.StorageProofBucketType
	}{
		{"current_block", 1000, 1000, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT},
		{"recent_inside", 1000, 1000 + int64(recent) - 1, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT},
		{"recent_boundary", 1000, 1000 + int64(recent), audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT},
		{"middle_just_after_recent", 1000, 1000 + int64(recent) + 1, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED},
		{"middle_just_before_old", 1000, 1000 + int64(old) - 1, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED},
		{"old_boundary", 1000, 1000 + int64(old), audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD},
		{"old_far", 1000, 1000 + int64(old) + 5000, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD},
		{"future_anchor_falls_through", 2000, 1000, audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyTicketBucket(tc.now, tc.anchor, recent, old)
			if got != tc.want {
				t.Fatalf("anchor=%d now=%d → %v, want %v", tc.anchor, tc.now, got, tc.want)
			}
		})
	}
}

func TestSelectTicketForBucket_DeterministicAndExcludes(t *testing.T) {
	tickets := []string{"t1", "t2", "t3", "t4", "t5"}
	bucket := audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT
	a := SelectTicketForBucket(tickets, nil, chainSeed, "sn-target", bucket)
	if a == "" {
		t.Fatal("expected a ticket, got empty")
	}
	for i := 0; i < 50; i++ {
		b := SelectTicketForBucket(tickets, nil, chainSeed, "sn-target", bucket)
		if a != b {
			t.Fatalf("non-deterministic ticket selection: %s vs %s on run %d", a, b, i)
		}
	}
	// Exclude the chosen one — must pick a different ticket.
	excl := map[string]struct{}{a: {}}
	b := SelectTicketForBucket(tickets, excl, chainSeed, "sn-target", bucket)
	if b == "" || b == a {
		t.Fatalf("exclusion broken: a=%s, b=%s", a, b)
	}
	// Exclude all — must yield empty.
	allExcluded := map[string]struct{}{}
	for _, t := range tickets {
		allExcluded[t] = struct{}{}
	}
	c := SelectTicketForBucket(tickets, allExcluded, chainSeed, "sn-target", bucket)
	if c != "" {
		t.Fatalf("expected empty when all excluded, got %s", c)
	}
	// Different bucket → may pick a different ticket (and must be deterministic).
	d := SelectTicketForBucket(tickets, nil, chainSeed, "sn-target", audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD)
	if d == "" {
		t.Fatal("OLD bucket should also produce a ticket")
	}
	d2 := SelectTicketForBucket(tickets, nil, chainSeed, "sn-target", audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD)
	if d != d2 {
		t.Fatalf("OLD bucket non-deterministic: %s vs %s", d, d2)
	}
	// Empty tickets → empty result, no panic.
	if got := SelectTicketForBucket(nil, nil, chainSeed, "sn-target", bucket); got != "" {
		t.Fatalf("nil input should give empty, got %s", got)
	}
}

func TestSelectTicketForBucket_UsesTicketRankDomainSeparator(t *testing.T) {
	tickets := []string{"t1", "t2"}
	got := SelectTicketForBucket(tickets, nil, chainSeed, "sn-target", audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT)
	if got != "t2" {
		t.Fatalf("expected ticket_rank-domain selection t2, got %s", got)
	}

	wantRank := storageTruthAssignmentHash(chainSeed, "sn-target", "RECENT", "t2", domainTicketRank)
	withoutDomain := storageTruthAssignmentHash(chainSeed, "sn-target", "RECENT", "t2")
	if bytes.Equal(wantRank, withoutDomain) {
		t.Fatalf("ticket_rank domain separator must change the ticket ranking hash")
	}
}

func TestSelectArtifactClass_WeightedDistribution(t *testing.T) {
	// 20% INDEX, 80% SYMBOL over many ticket draws.
	indexN, symbolN := 0, 0
	for i := 0; i < 5000; i++ {
		ticket := "t-" + ifmt(i)
		c := SelectArtifactClass(chainSeed, "sn-target", ticket, 100, 100)
		switch c {
		case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:
			indexN++
		case audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:
			symbolN++
		default:
			t.Fatalf("unexpected class %v on draw %d", c, i)
		}
	}
	total := indexN + symbolN
	idxFrac := float64(indexN) / float64(total)
	// Expected 0.2, allow ±2% tolerance for 5000 draws.
	if idxFrac < 0.18 || idxFrac > 0.22 {
		t.Fatalf("INDEX fraction %.4f outside expected 0.18-0.22 (n=%d/%d)", idxFrac, indexN, total)
	}
}

func TestSelectArtifactClass_FallbackWhenClassEmpty(t *testing.T) {
	// indexCount=0 → must always return SYMBOL even when roll wants INDEX.
	for i := 0; i < 100; i++ {
		c := SelectArtifactClass(chainSeed, "sn-target", "t-"+ifmt(i), 0, 50)
		if c != audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL {
			t.Fatalf("with indexCount=0, must fall back to SYMBOL; got %v", c)
		}
	}
	// symbolCount=0 → always INDEX.
	for i := 0; i < 100; i++ {
		c := SelectArtifactClass(chainSeed, "sn-target", "t-"+ifmt(i), 50, 0)
		if c != audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX {
			t.Fatalf("with symbolCount=0, must fall back to INDEX; got %v", c)
		}
	}
	// Both zero → UNSPECIFIED.
	if c := SelectArtifactClass(chainSeed, "sn-target", "t1", 0, 0); c != audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED {
		t.Fatalf("with both zero, expected UNSPECIFIED; got %v", c)
	}
}

func TestSelectArtifactOrdinal_BoundsAndDeterminism(t *testing.T) {
	const count = 64
	first, err := SelectArtifactOrdinal(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, count)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if first >= count {
		t.Fatalf("ordinal out of range: %d", first)
	}
	for i := 0; i < 50; i++ {
		again, _ := SelectArtifactOrdinal(chainSeed, "sn-target", "ticket-1",
			audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, count)
		if again != first {
			t.Fatalf("non-deterministic: %d vs %d", first, again)
		}
	}
	// Errors:
	if _, err := SelectArtifactOrdinal(chainSeed, "sn-target", "t",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 0); err == nil {
		t.Fatal("expected error for count=0")
	}
	if _, err := SelectArtifactOrdinal(chainSeed, "sn-target", "t",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED, 5); err == nil {
		t.Fatal("expected error for unspecified class")
	}
}

func TestComputeMultiRangeOffsets_AllInBounds(t *testing.T) {
	const size, rl = uint64(10000), uint64(256)
	offsets, err := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 0, size, rl, 4)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(offsets) != 4 {
		t.Fatalf("expected 4 offsets, got %d", len(offsets))
	}
	for i, off := range offsets {
		if off+rl > size {
			t.Fatalf("offset %d (%d + %d) exceeds size %d", i, off, rl, size)
		}
	}
	// Determinism.
	o2, _ := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 0, size, rl, 4)
	if !equalSliceUint64(offsets, o2) {
		t.Fatalf("non-deterministic offsets: %v vs %v", offsets, o2)
	}
}

func TestComputeMultiRangeOffsets_OffsetsDistinctOnDifferentInputs(t *testing.T) {
	const size, rl = uint64(10000), uint64(256)
	a, _ := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 0, size, rl, 4)
	b, _ := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-2",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 0, size, rl, 4)
	if equalSliceUint64(a, b) {
		t.Fatalf("different ticket should change offsets, got identical %v", a)
	}
	c, _ := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX, 0, size, rl, 4)
	if equalSliceUint64(a, c) {
		t.Fatalf("different class should change offsets, got identical %v", a)
	}
	d, _ := ComputeMultiRangeOffsets(chainSeed, "sn-target", "ticket-1",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL, 1, size, rl, 4)
	if equalSliceUint64(a, d) {
		t.Fatalf("different ordinal should change offsets, got identical %v", a)
	}
}

func TestComputeMultiRangeOffsets_Errors(t *testing.T) {
	cls := audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL
	if _, err := ComputeMultiRangeOffsets(chainSeed, "x", "t", cls, 0, 100, 256, 4); err == nil {
		t.Fatal("expected error when rangeLen >= artifactSize")
	}
	if _, err := ComputeMultiRangeOffsets(chainSeed, "x", "t", cls, 0, 1000, 256, 0); err == nil {
		t.Fatal("expected error for k=0")
	}
	if _, err := ComputeMultiRangeOffsets(chainSeed, "x", "t", cls, 0, 1000, 0, 4); err == nil {
		t.Fatal("expected error for rangeLen=0")
	}
	if _, err := ComputeMultiRangeOffsets(chainSeed, "x", "t",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED, 0, 1000, 256, 4); err == nil {
		t.Fatal("expected error for unspecified class")
	}
}

func TestComputeCompoundChallengeHash_KnownVector(t *testing.T) {
	// 1024-byte input filled with byte values 0..255 cycling.
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i)
	}
	offsets := []uint64{0, 256, 512, 768}
	rl := uint64(256)
	got, err := ComputeCompoundChallengeHash(data, offsets, rl)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Independent reference computation.
	h := blake3.New(32, nil)
	for _, off := range offsets {
		h.Write(data[off : off+rl])
	}
	var want [32]byte
	copy(want[:], h.Sum(nil))
	if got != want {
		t.Fatalf("compound hash mismatch\nwant %x\ngot  %x", want, got)
	}
}

func TestComputeCompoundChallengeHash_OrderMatters(t *testing.T) {
	// Build data where each 256-byte slice has a unique signature: fill with
	// `byte(off >> 8)` so slice [0:256] = 0x00…, [256:512] = 0x01…, etc.
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i >> 8)
	}
	a, _ := ComputeCompoundChallengeHash(data, []uint64{0, 256, 512, 768}, 256)
	b, _ := ComputeCompoundChallengeHash(data, []uint64{768, 512, 256, 0}, 256)
	if a == b {
		t.Fatal("compound hash should be order-sensitive (slices concatenated in offset order)")
	}
}

func TestComputeCompoundChallengeHash_OutOfBounds(t *testing.T) {
	data := make([]byte, 100)
	if _, err := ComputeCompoundChallengeHash(data, []uint64{50}, 60); err == nil {
		t.Fatal("expected error for out-of-bounds slice")
	}
	if _, err := ComputeCompoundChallengeHash(data, []uint64{100}, 1); err == nil {
		t.Fatal("expected error when offset == len(data)")
	}
}

func TestDerivationInputHash_DeterministicAndSensitive(t *testing.T) {
	cls := audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL
	a, err := DerivationInputHash(chainSeed, "sn-target", "ticket-1", cls, 7, []uint64{1, 2, 3, 4}, 256)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-char hex, got %d (%q)", len(a), a)
	}
	b, _ := DerivationInputHash(chainSeed, "sn-target", "ticket-1", cls, 7, []uint64{1, 2, 3, 4}, 256)
	if a != b {
		t.Fatalf("non-deterministic: %s vs %s", a, b)
	}
	// Each input field must change the hash.
	cases := []struct {
		name string
		fn   func() (string, error)
	}{
		{"ticket", func() (string, error) {
			return DerivationInputHash(chainSeed, "sn-target", "ticket-2", cls, 7, []uint64{1, 2, 3, 4}, 256)
		}},
		{"ordinal", func() (string, error) {
			return DerivationInputHash(chainSeed, "sn-target", "ticket-1", cls, 8, []uint64{1, 2, 3, 4}, 256)
		}},
		{"offsets", func() (string, error) {
			return DerivationInputHash(chainSeed, "sn-target", "ticket-1", cls, 7, []uint64{1, 2, 3, 5}, 256)
		}},
		{"rangeLen", func() (string, error) {
			return DerivationInputHash(chainSeed, "sn-target", "ticket-1", cls, 7, []uint64{1, 2, 3, 4}, 257)
		}},
		{"target", func() (string, error) {
			return DerivationInputHash(chainSeed, "other-target", "ticket-1", cls, 7, []uint64{1, 2, 3, 4}, 256)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := tc.fn()
			if got == a {
				t.Fatalf("changing %s did not change hash (%s)", tc.name, got)
			}
		})
	}
}

func TestTranscriptHash_DeterministicAndSensitive(t *testing.T) {
	in := TranscriptInputs{
		EpochID:                    42,
		ChallengerSupernodeAccount: "sn-prober",
		TargetSupernodeAccount:     "sn-target",
		TicketID:                   "ticket-1",
		Bucket:                     audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT,
		ArtifactClass:              audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL,
		ArtifactOrdinal:            3,
		ArtifactKey:                "p2p-key-abc",
		DerivationInputHash:        "deadbeef",
		CompoundProofHashHex:       "feedface",
		ObserverIDs:                []string{"sn-obs-1", "sn-obs-2"},
	}
	a, err := TranscriptHash(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64-char hex, got %d", len(a))
	}
	// Determinism even when observers are in shuffled input order.
	in2 := in
	in2.ObserverIDs = []string{"sn-obs-2", "sn-obs-1"}
	b, _ := TranscriptHash(in2)
	if a != b {
		t.Fatalf("observer order should be normalised: %s vs %s", a, b)
	}

	// NO_ELIGIBLE_TICKET shape (ticket_id == "" with UNSPECIFIED class) is allowed.
	noTicket := TranscriptInputs{
		EpochID:                    42,
		ChallengerSupernodeAccount: "sn-prober",
		TargetSupernodeAccount:     "sn-target",
		TicketID:                   "",
		Bucket:                     audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT,
		ArtifactClass:              audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED,
		ObserverIDs:                []string{"sn-obs-1"},
	}
	if _, err := TranscriptHash(noTicket); err != nil {
		t.Fatalf("NO_ELIGIBLE_TICKET shape should be valid, err: %v", err)
	}

	// UNSPECIFIED class with non-empty ticket → error.
	bad := in
	bad.ArtifactClass = audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED
	if _, err := TranscriptHash(bad); err == nil {
		t.Fatal("expected error for UNSPECIFIED class with ticket")
	}

	// Each major input must change the hash.
	cases := []func(*TranscriptInputs){
		func(x *TranscriptInputs) { x.EpochID = 43 },
		func(x *TranscriptInputs) { x.ChallengerSupernodeAccount = "sn-other-prober" },
		func(x *TranscriptInputs) { x.TargetSupernodeAccount = "sn-other-target" },
		func(x *TranscriptInputs) { x.TicketID = "ticket-2" },
		func(x *TranscriptInputs) { x.Bucket = audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD },
		func(x *TranscriptInputs) { x.ArtifactOrdinal = 4 },
		func(x *TranscriptInputs) { x.ArtifactKey = "p2p-key-other" },
		func(x *TranscriptInputs) { x.DerivationInputHash = "00" },
		func(x *TranscriptInputs) { x.CompoundProofHashHex = "11" },
		func(x *TranscriptInputs) { x.ObserverIDs = []string{"sn-obs-3"} },
	}
	for i, mut := range cases {
		x := in
		mut(&x)
		got, _ := TranscriptHash(x)
		if got == a {
			t.Fatalf("mutation %d did not change transcript hash", i)
		}
	}
}

func TestTranscriptHash_UnsupportedBucket(t *testing.T) {
	in := TranscriptInputs{
		Bucket: audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED,
	}
	if _, err := TranscriptHash(in); err == nil {
		t.Fatal("expected error for UNSPECIFIED bucket")
	}
}

func TestArtifactClassDomain_Stable(t *testing.T) {
	cases := map[audittypes.StorageProofArtifactClass]string{
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:       "INDEX",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL:      "SYMBOL",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED: "",
	}
	for cls, want := range cases {
		got := ArtifactClassDomain(cls)
		if got != want {
			t.Fatalf("ArtifactClassDomain(%v) = %q, want %q", cls, got, want)
		}
	}
}

func TestBucketDomain_Stable(t *testing.T) {
	cases := map[audittypes.StorageProofBucketType]string{
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT:      "RECENT",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD:         "OLD",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_PROBATION:   "PROBATION",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECHECK:     "RECHECK",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED: "",
	}
	for b, want := range cases {
		if got := BucketDomain(b); got != want {
			t.Fatalf("BucketDomain(%v) = %q, want %q", b, got, want)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func equalSliceOrdered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalSliceUint64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ifmt is a tiny non-allocating int-to-decimal-string we use only inside
// distribution tests where strconv.Itoa would still be fine — kept minimal so
// the test file has no extra imports beyond the production package's.
func ifmt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// Stable cross-platform sort.Strings sanity check (catches accidental
// reliance on platform-specific stable-sort behaviour in CI).
func TestSortStrings_StableForPairs(t *testing.T) {
	xs := []string{"sn-c", "sn-a", "sn-b"}
	sort.Strings(xs)
	want := []string{"sn-a", "sn-b", "sn-c"}
	if !equalSliceOrdered(xs, want) {
		t.Fatalf("stable sort mismatch: %v != %v", xs, want)
	}
}
