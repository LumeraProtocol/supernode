// LEP-6 deterministic primitives.
//
// This file implements the off-chain computation library that the supernode's
// storage_challenge runtime, recheck service, and self-healing dispatcher all
// share. Every function in this file is pure: same inputs always yield the same
// output, no I/O, no goroutines, no clock.
//
// # Why "deterministic" matters
//
// LEP-6 distributes one compound storage challenge per epoch to a deterministic
// 1/3 subset of active supernodes. Multiple independent reporters (probers)
// must agree byte-for-byte on:
//
//   - which supernodes are challenged this epoch (target set),
//   - which (challenger, target) pair an individual reporter is assigned to,
//   - which ticket is selected per bucket,
//   - which artifact (class, ordinal, key) is challenged,
//   - which byte ranges are sampled,
//   - the resulting transcript identifiers.
//
// If any one of those steps diverges between two supernodes, their
// StorageProofResults will not match and the chain will treat them as
// contradictions — penalising both. This package is therefore the single
// canonical implementation that every supernode must run.
//
// # Chain-mirrored vs supernode-canonical primitives
//
// Two primitives MUST mirror the chain byte-for-byte because the chain itself
// runs them to compute the assignment for `MsgSubmitEpochReport` validation:
//
//   - SelectLEP6Targets — mirrors lumera/x/audit/v1/keeper/audit_peer_assignment.go
//     (rankStorageTruthAccounts, label "challenge_target")
//   - PairChallengerToTarget — mirrors the inline pair-ranking loop in the
//     same file (label "pair")
//
// Both use SHA-256 with the byte composition documented on
// storageTruthAssignmentHash below — exactly matching
// lumera/x/audit/v1/keeper/audit_peer_assignment.go:232.
//
// All other primitives (ticket / class / ordinal selection, multi-range
// offsets, compound hash, derivation input hash, transcript hash) are NOT
// computed on the chain. The chain stores their outputs as opaque strings and
// only validates structural fields (non-empty, ordinal < count, class ∈
// {INDEX, SYMBOL}, etc.). The supernode is therefore the canonical source for
// these encodings. To keep all reporters in lockstep, every supernode must use
// the byte schema defined here. Changes to these schemas are protocol-level
// changes that require coordination across the network — do not adjust them
// without bumping a versioned domain separator.
//
// # Reference test vectors
//
//   - audit_peer_assignment_test.go::TestStorageTruthAssignmentUsesOneThirdCoverage
//     uses seed=[]byte("01234567890123456789012345678901") with active set
//     {sn-a..sn-f} and divisor=3, expecting targetCount=2. Reproduced as
//     TestSelectLEP6Targets_OneThirdCoverage_AssignmentMatchesChain.
package deterministic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sort"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"lukechampine.com/blake3"
)

// LEP6 default constants.
//
// Mirrors lumera/x/audit/v1/types/params.go defaults; duplicated here so the
// supernode can compute primitives without round-tripping to the chain when
// the operator has not overridden the relevant params.
const (
	// LEP6CompoundRangesPerArtifact is k in LEP-6 §11.
	LEP6CompoundRangesPerArtifact = 4
	// LEP6CompoundRangeLenBytes is range_len in LEP-6 §11.
	LEP6CompoundRangeLenBytes = 256
	// LEP6ChallengeTargetDivisor selects 1/3 of active supernodes per epoch.
	LEP6ChallengeTargetDivisor = 3
	// LEP6ArtifactClassRollModulus is the divisor for the §10 class roll
	// (0..1 -> INDEX, 2..9 -> SYMBOL).
	LEP6ArtifactClassRollModulus = 10
	// LEP6ArtifactClassIndexCutoff is exclusive upper bound for INDEX bucket
	// (roll < cutoff -> INDEX).
	LEP6ArtifactClassIndexCutoff = 2
)

// Domain separator labels used across LEP-6 hash inputs. Freezing these as
// constants prevents accidental drift between callers and tests.
const (
	domainTargetRank        = "challenge_target"
	domainPairRank          = "pair"
	domainTicketRank        = "ticket_rank"
	domainArtifactClass    = "artifact_class"
	domainArtifactOrdinal  = "artifact_ordinal"
	domainRangeOffset       = "range_offset"
	domainDerivationInput   = "derivation_input"
	domainTranscript        = "transcript"
)

// Stable string forms for proto enums that participate in hash inputs. We
// deliberately use the short proto suffix (INDEX/SYMBOL, RECENT/OLD/...) — not
// the integer varint, which is brittle if the proto enum is ever renumbered,
// and not the full SCREAMING_SNAKE String() form, which is verbose. Once
// frozen, these strings become part of the protocol surface.
var (
	artifactClassDomain = map[audittypes.StorageProofArtifactClass]string{
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX:  "INDEX",
		audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL: "SYMBOL",
	}
	bucketDomain = map[audittypes.StorageProofBucketType]string{
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT:    "RECENT",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD:       "OLD",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_PROBATION: "PROBATION",
		audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECHECK:   "RECHECK",
	}
)

// ArtifactClassDomain returns the canonical hash-input string for the given
// artifact class, or empty string if the class is unspecified or unknown. It
// is exported because PR3+ callers may need to verify a freshly-decoded enum
// has a stable domain string before proceeding.
func ArtifactClassDomain(class audittypes.StorageProofArtifactClass) string {
	return artifactClassDomain[class]
}

// BucketDomain returns the canonical hash-input string for the given bucket
// type, or empty string if the bucket is unspecified or unknown.
func BucketDomain(bucket audittypes.StorageProofBucketType) string {
	return bucketDomain[bucket]
}

// storageTruthAssignmentHash mirrors
// lumera/x/audit/v1/keeper/audit_peer_assignment.go:232 byte-for-byte. It
// computes:
//
//	SHA-256(seed || 0x00 || part_0 || 0x00 || part_1 || ... || 0x00 || part_n)
//
// with a NUL byte written before EACH part (not between parts; not after the
// seed alone). No length prefix, no trailing terminator, raw UTF-8 of each
// part string. This is the exact composition the chain validates against.
func storageTruthAssignmentHash(seed []byte, parts ...string) []byte {
	h := sha256.New()
	_, _ = h.Write(seed)
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return h.Sum(nil)
}

// rankedAccount carries an account/id paired with its sort key.
type rankedAccount struct {
	id   string
	rank []byte
}

// rankAccounts is the package-internal helper used by SelectLEP6Targets and
// (indirectly) by PairChallengerToTarget. It mirrors rankStorageTruthAccounts
// at audit_peer_assignment.go:214–230 — sort ascending by rank with lex
// tiebreak on id.
func rankAccounts(seed []byte, accounts []string, label string) []rankedAccount {
	ranked := make([]rankedAccount, len(accounts))
	for i, a := range accounts {
		ranked[i] = rankedAccount{
			id:   a,
			rank: storageTruthAssignmentHash(seed, a, label),
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if c := compareBytes(ranked[i].rank, ranked[j].rank); c != 0 {
			return c < 0
		}
		return ranked[i].id < ranked[j].id
	})
	return ranked
}

// SelectLEP6Targets returns the deterministic challenge-target subset for the
// given epoch seed. Mirrors the chain's per-account ranking exactly:
// targetCount = ceil(len(activeIDs) / divisor), clamped to [1, len(activeIDs)].
//
// The activeIDs slice is treated as the candidate set as-is. Callers that
// need to deduplicate / sort first must do so before calling — the chain
// itself feeds in `sortedUniqueStrings(activeSorted)` and then takes the
// intersection with explicit `targetsSorted`. For supernode purposes the
// active supernode list is already canonicalised by the chain, so the caller
// typically passes that list straight through.
//
// If divisor is zero, the LEP-6 default (3) is used so partial param overrides
// don't produce 1-target-per-supernode coverage by accident.
func SelectLEP6Targets(activeIDs []string, seed []byte, divisor uint32) []string {
	if len(activeIDs) == 0 {
		return nil
	}
	if divisor == 0 {
		divisor = LEP6ChallengeTargetDivisor
	}
	count := (len(activeIDs) + int(divisor) - 1) / int(divisor)
	if count < 1 {
		count = 1
	}
	if count > len(activeIDs) {
		count = len(activeIDs)
	}
	ranked := rankAccounts(seed, activeIDs, domainTargetRank)
	out := make([]string, count)
	for i := 0; i < count; i++ {
		out[i] = ranked[i].id
	}
	return out
}

// PairChallengerToTarget assigns one target from `targets` to the given
// challenger using the chain's pair-ranking algorithm.
//
// The chain assigns targets in iteration order over sorted unique active
// challengers, picking for each challenger the smallest-rank unassigned
// target (ties broken lex on target id), and a challenger never gets itself
// as a target. This function reproduces that loop deterministically: for the
// caller's challenger, it selects the unassigned target with smallest pair
// rank that is not equal to the caller, treating `assigned` as the set of
// targets already taken by lower-ranked challengers in the same epoch.
//
// `assigned` may be nil. If non-nil, the function will not return any target
// already present in it. The caller is expected to feed in the fixed-iteration
// view of the assignment as the chain computes it (see
// SelectLEP6Targets + iterate through challengers in deterministic order).
//
// Returns "" if no valid target remains for this challenger.
func PairChallengerToTarget(challenger string, targets []string, seed []byte, assigned map[string]struct{}) string {
	bestTarget := ""
	var bestRank []byte
	for _, t := range targets {
		if t == challenger {
			continue
		}
		if assigned != nil {
			if _, taken := assigned[t]; taken {
				continue
			}
		}
		rank := storageTruthAssignmentHash(seed, challenger, t, domainPairRank)
		if bestTarget == "" {
			bestTarget = t
			bestRank = rank
			continue
		}
		c := compareBytes(rank, bestRank)
		if c < 0 || (c == 0 && t < bestTarget) {
			bestTarget = t
			bestRank = rank
		}
	}
	return bestTarget
}

// AssignChallengerTargets reproduces the full chain-side challenger→target
// pairing for the entire active set. It is provided for tests and for callers
// who need the complete map (e.g. observability emit paths). For runtime use
// the chain's QueryAssignedTargets is the canonical source — call this only
// when chain access is unavailable or for cross-checking.
//
// Iteration order is the lexicographic order of `activeIDs`, matching the
// chain's `sortedUniqueStrings(activeSorted)` precondition. Callers who need
// to feed in an already-sorted unique slice may; otherwise the function does
// not modify its inputs.
func AssignChallengerTargets(activeIDs, targets []string, seed []byte) map[string]string {
	if len(activeIDs) == 0 || len(targets) == 0 {
		return map[string]string{}
	}
	// Sort lexicographically without mutating caller's slice.
	challengers := append([]string(nil), activeIDs...)
	sort.Strings(challengers)
	assigned := make(map[string]struct{}, len(targets))
	out := make(map[string]string, len(challengers))
	for _, c := range challengers {
		if len(assigned) >= len(targets) {
			break
		}
		t := PairChallengerToTarget(c, targets, seed, assigned)
		if t == "" {
			continue
		}
		assigned[t] = struct{}{}
		out[c] = t
	}
	return out
}

// ClassifyTicketBucket classifies a ticket into a LEP-6 bucket based on its
// on-chain anchor height. Per LEP-6 §8 (and the chain's bucket-default fix in
// `LEP-6-consensus-gap-fixes`), bucket boundaries derive from the chain's
// epoch span:
//
//   - RECENT  if currentHeight - anchorHeight <= recentBucketMaxBlocks (default 3 * epoch_length_blocks)
//   - OLD     if currentHeight - anchorHeight >= oldBucketMinBlocks    (default 30 * epoch_length_blocks)
//   - else    UNSPECIFIED (middle bucket — eligible only for rechecks /
//             probation per LEP-6 §8)
//
// The "anchor height" is the cascade Action's BlockHeight (set at
// RegisterAction; not updated at finalization). Action.UpdatedHeight does not
// exist — see PR2 implementation note in
// docs/plans/LEP6_SUPERNODE_IMPLEMENTATION_PLAN.md §"Resolved Decision 3".
//
// If currentHeight < anchorHeight (clock-skew or replay scenarios), the
// classification falls through to UNSPECIFIED.
func ClassifyTicketBucket(currentHeight, anchorHeight int64, recentBucketMaxBlocks, oldBucketMinBlocks uint64) audittypes.StorageProofBucketType {
	if currentHeight < anchorHeight {
		return audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED
	}
	delta := uint64(currentHeight - anchorHeight)
	if delta <= recentBucketMaxBlocks {
		return audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT
	}
	if delta >= oldBucketMinBlocks {
		return audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_OLD
	}
	return audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_UNSPECIFIED
}

// SelectTicketForBucket picks one ticket deterministically for a given
// (target, bucket) pair from a pool of eligible tickets, excluding any tickets
// the caller marks as ineligible (e.g. tickets with an active heal op per
// LEP-6 §9 step 2).
//
// Rank: SHA-256(seed || 0x00 || target || 0x00 || bucket || 0x00 || ticket_id)
// — where bucket is the BucketDomain() string ("RECENT", "OLD", "PROBATION",
// or "RECHECK"). Ascending sort, lex tiebreak on ticket id.
//
// Returns "" if no eligible ticket remains, signalling NO_ELIGIBLE_TICKET to
// the caller per LEP-6 §9.
func SelectTicketForBucket(eligibleTicketIDs []string, excluded map[string]struct{}, seed []byte, target string, bucket audittypes.StorageProofBucketType) string {
	bucketStr := BucketDomain(bucket)
	if bucketStr == "" {
		return ""
	}
	bestTicket := ""
	var bestRank []byte
	for _, t := range eligibleTicketIDs {
		if t == "" {
			continue
		}
		if excluded != nil {
			if _, skip := excluded[t]; skip {
				continue
			}
		}
		rank := storageTruthAssignmentHash(seed, target, bucketStr, t)
		if bestTicket == "" {
			bestTicket = t
			bestRank = rank
			continue
		}
		c := compareBytes(rank, bestRank)
		if c < 0 || (c == 0 && t < bestTicket) {
			bestTicket = t
			bestRank = rank
		}
	}
	return bestTicket
}

// SelectArtifactClass implements the LEP-6 §10 step 1 class roll:
//
//	class_roll = SHA-256(seed || 0x00 || target || 0x00 || ticket_id || 0x00 || "artifact_class")[:8] (big-endian uint64) mod 10
//	class_roll < 2 -> INDEX, else SYMBOL
//
// If the chosen class has zero artifacts, the function falls back
// deterministically to the other class. If neither class has any artifacts,
// returns UNSPECIFIED — the caller should record NO_ELIGIBLE_TICKET.
func SelectArtifactClass(seed []byte, target, ticketID string, indexCount, symbolCount uint32) audittypes.StorageProofArtifactClass {
	if indexCount == 0 && symbolCount == 0 {
		return audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_UNSPECIFIED
	}
	rollHash := storageTruthAssignmentHash(seed, target, ticketID, domainArtifactClass)
	roll := binary.BigEndian.Uint64(rollHash[:8]) % LEP6ArtifactClassRollModulus
	preferIndex := roll < LEP6ArtifactClassIndexCutoff
	if preferIndex {
		if indexCount > 0 {
			return audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX
		}
		return audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL
	}
	if symbolCount > 0 {
		return audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL
	}
	return audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_INDEX
}

// SelectArtifactOrdinal implements LEP-6 §10 step 2:
//
//	artifact_ordinal = SHA-256(seed || 0x00 || target || 0x00 || ticket_id || 0x00 || class_domain || 0x00 || "artifact_ordinal")[:8] (big-endian uint64) mod artifactCount
//
// Returns an error if artifactCount is zero (caller must have already
// validated the class has artifacts via SelectArtifactClass). Returns an
// error for unsupported classes.
func SelectArtifactOrdinal(seed []byte, target, ticketID string, class audittypes.StorageProofArtifactClass, artifactCount uint32) (uint32, error) {
	if artifactCount == 0 {
		return 0, fmt.Errorf("deterministic.SelectArtifactOrdinal: artifactCount must be > 0")
	}
	classDomain := ArtifactClassDomain(class)
	if classDomain == "" {
		return 0, fmt.Errorf("deterministic.SelectArtifactOrdinal: unsupported class %v", class)
	}
	h := storageTruthAssignmentHash(seed, target, ticketID, classDomain, domainArtifactOrdinal)
	return uint32(binary.BigEndian.Uint64(h[:8]) % uint64(artifactCount)), nil
}

// ComputeMultiRangeOffsets produces the LEP-6 §11 deterministic byte-range
// offsets for a single artifact challenge:
//
//	offset_i = SHA-256(seed || 0x00 || target || 0x00 || ticket_id ||
//	                   0x00 || class_domain || 0x00 || u32be(ordinal) ||
//	                   0x00 || u32be(i))[:8] (big-endian uint64)
//	          mod (artifactSize - rangeLen)
//
// Defaults: k=4 ranges, rangeLen=256 bytes (LEP-6 spec values). Both must be
// passed explicitly so a future param change at the chain level can be
// surfaced cleanly. The returned slice has length exactly k.
//
// Returns an error if rangeLen >= artifactSize (would yield negative modulus
// space) or if any input is degenerate (k=0, empty class).
//
// IMPORTANT — BYTE-ENCODING COMMITMENT (PROTOCOL FROZEN): ordinal is encoded
// as raw u32be (4 bytes), the range index `i` as raw u32be (4 bytes), and
// artifactSize / rangeLen / offset values as raw u64be (8 bytes) — never as
// decimal-string forms. The domain separator "compound_range" (see
// ComputeCompoundChallengeHash below) locks this schema against alternative
// encodings. If you change any width or representation, you MUST version the
// domain separator and update the protocol guide; otherwise chain and
// supernode will silently disagree on offsets. Per LEP-6 Plan v2.
func ComputeMultiRangeOffsets(seed []byte, target, ticketID string, class audittypes.StorageProofArtifactClass, ordinal uint32, artifactSize, rangeLen uint64, k int) ([]uint64, error) {
	if k <= 0 {
		return nil, fmt.Errorf("deterministic.ComputeMultiRangeOffsets: k must be > 0")
	}
	if rangeLen == 0 {
		return nil, fmt.Errorf("deterministic.ComputeMultiRangeOffsets: rangeLen must be > 0")
	}
	if artifactSize <= rangeLen {
		return nil, fmt.Errorf("deterministic.ComputeMultiRangeOffsets: artifactSize (%d) must be > rangeLen (%d)", artifactSize, rangeLen)
	}
	classDomain := ArtifactClassDomain(class)
	if classDomain == "" {
		return nil, fmt.Errorf("deterministic.ComputeMultiRangeOffsets: unsupported class %v", class)
	}
	span := artifactSize - rangeLen
	offsets := make([]uint64, k)
	var ordBuf, idxBuf [4]byte
	binary.BigEndian.PutUint32(ordBuf[:], ordinal)
	for i := 0; i < k; i++ {
		binary.BigEndian.PutUint32(idxBuf[:], uint32(i))
		// We deliberately reach into the same `seed || 0x00 || part || ...`
		// composition by passing the binary parts as Go strings (the helper
		// takes string parts, but Go strings are byte sequences and may
		// contain arbitrary bytes including NULs without issue here — the
		// helper writes []byte(part) raw).
		h := storageTruthAssignmentHash(seed,
			target,
			ticketID,
			classDomain,
			string(ordBuf[:]),
			string(idxBuf[:]),
			domainRangeOffset,
		)
		offsets[i] = binary.BigEndian.Uint64(h[:8]) % span
	}
	return offsets, nil
}

// ComputeCompoundChallengeHash computes the BLAKE3-256 hash of the
// concatenation of `len(offsets)` byte ranges, each `rangeLen` bytes long, at
// the given offsets within `data`. This is the proof-construction primitive
// per LEP-6 §11:
//
//	challenge_hash = blake3(slice_0 || slice_1 || ... || slice_{k-1})
//
// Slices are read in the order provided (NOT sorted) so the caller's chosen
// offset ordering — which matches ComputeMultiRangeOffsets' i=0..k-1 — is
// preserved and reproducible by observers.
//
// Returns an error if any offset+rangeLen exceeds len(data).
func ComputeCompoundChallengeHash(data []byte, offsets []uint64, rangeLen uint64) ([32]byte, error) {
	var zero [32]byte
	if rangeLen == 0 {
		return zero, fmt.Errorf("deterministic.ComputeCompoundChallengeHash: rangeLen must be > 0")
	}
	if rangeLen > math.MaxInt {
		return zero, fmt.Errorf("deterministic.ComputeCompoundChallengeHash: rangeLen too large")
	}
	dataLen := uint64(len(data))
	h := blake3.New(32, nil)
	for i, off := range offsets {
		end := off + rangeLen
		if end < off || end > dataLen {
			return zero, fmt.Errorf("deterministic.ComputeCompoundChallengeHash: range %d (offset=%d len=%d) exceeds data size %d", i, off, rangeLen, dataLen)
		}
		_, _ = h.Write(data[off:end])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// DerivationInputHash produces the canonical hex string the supernode submits
// as `StorageProofResult.derivation_input_hash`. The chain stores it as an
// opaque non-empty string and uses it for transcript indexing only; this
// function defines the canonical encoding so that two reporters challenging
// the same (target, ticket, class, ordinal, offsets) combination produce
// identical hashes.
//
// Encoding:
//
//	SHA-256(seed || 0x00 || target || 0x00 || ticket_id || 0x00 ||
//	        class_domain || 0x00 || u32be(ordinal) || 0x00 ||
//	        u64be(rangeLen) || 0x00 || u64be(offset_0) || ... ||
//	        0x00 || u64be(offset_{k-1}) || 0x00 || "derivation_input")
//
// Returned as lowercase hex (no 0x prefix, length 64).
func DerivationInputHash(seed []byte, target, ticketID string, class audittypes.StorageProofArtifactClass, ordinal uint32, offsets []uint64, rangeLen uint64) (string, error) {
	classDomain := ArtifactClassDomain(class)
	if classDomain == "" {
		return "", fmt.Errorf("deterministic.DerivationInputHash: unsupported class %v", class)
	}
	parts := make([]string, 0, 4+len(offsets)+1)
	parts = append(parts, target, ticketID, classDomain)

	var ordBuf [4]byte
	binary.BigEndian.PutUint32(ordBuf[:], ordinal)
	parts = append(parts, string(ordBuf[:]))

	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], rangeLen)
	parts = append(parts, string(lenBuf[:]))

	for _, off := range offsets {
		var offBuf [8]byte
		binary.BigEndian.PutUint64(offBuf[:], off)
		parts = append(parts, string(offBuf[:]))
	}
	parts = append(parts, domainDerivationInput)

	return hex.EncodeToString(storageTruthAssignmentHash(seed, parts...)), nil
}

// TranscriptInputs bundles the fields that go into TranscriptHash. Using a
// struct keeps the call site readable and makes the input ordering explicit —
// any caller who tries to reorder fields will hit the field-name resolution
// at compile time, not at hash-compare time.
type TranscriptInputs struct {
	EpochID                  uint64
	ChallengerSupernodeAccount string
	TargetSupernodeAccount     string
	TicketID                   string
	Bucket                     audittypes.StorageProofBucketType
	ArtifactClass              audittypes.StorageProofArtifactClass
	ArtifactOrdinal            uint32
	ArtifactKey                string
	DerivationInputHash        string // hex from DerivationInputHash (or empty for NO_ELIGIBLE_TICKET)
	CompoundProofHashHex       string // hex of ComputeCompoundChallengeHash output (or empty for NO_ELIGIBLE_TICKET)
	ObserverIDs                []string
}

// TranscriptHash produces the canonical hex string the supernode submits as
// `StorageProofResult.transcript_hash`.
//
// Encoding:
//
//	SHA-256(seed=u64be(epoch_id) || 0x00 || challenger || 0x00 || target ||
//	        0x00 || ticket_id || 0x00 || bucket_domain || 0x00 ||
//	        class_domain || 0x00 || u32be(ordinal) || 0x00 || artifact_key ||
//	        0x00 || derivation_input_hash || 0x00 || compound_proof_hash_hex ||
//	        0x00 || u32be(len(observer_ids)) ||
//	        0x00 || observer_id_0 || ... || 0x00 || observer_id_n-1 ||
//	        0x00 || "transcript")
//
// Note that the "seed" passed into storageTruthAssignmentHash here is
// u64be(epoch_id) — not the epoch anchor seed — because transcripts are
// epoch-scoped identifiers and the epoch anchor seed already commits to the
// chain state at that height. Including the epoch id directly keeps the
// transcript portable across replay scenarios.
//
// Observer ids are sorted lex before hashing so observer-set permutations do
// not produce different transcripts for the same logical proof.
//
// Returns lowercase hex.
func TranscriptHash(in TranscriptInputs) (string, error) {
	bucketDom := BucketDomain(in.Bucket)
	if bucketDom == "" {
		return "", fmt.Errorf("deterministic.TranscriptHash: unsupported bucket %v", in.Bucket)
	}
	// Class is allowed to be UNSPECIFIED only in the NO_ELIGIBLE_TICKET
	// transcript shape (TicketID == ""). For all other inputs we require a
	// known class.
	classDom := ArtifactClassDomain(in.ArtifactClass)
	if classDom == "" {
		if in.TicketID != "" {
			return "", fmt.Errorf("deterministic.TranscriptHash: unsupported class %v with non-empty ticket", in.ArtifactClass)
		}
		classDom = "UNSPECIFIED"
	}

	var epochSeed [8]byte
	binary.BigEndian.PutUint64(epochSeed[:], in.EpochID)

	var ordBuf [4]byte
	binary.BigEndian.PutUint32(ordBuf[:], in.ArtifactOrdinal)

	observers := append([]string(nil), in.ObserverIDs...)
	sort.Strings(observers)
	var obsCount [4]byte
	binary.BigEndian.PutUint32(obsCount[:], uint32(len(observers)))

	parts := make([]string, 0, 11+len(observers))
	parts = append(parts,
		in.ChallengerSupernodeAccount,
		in.TargetSupernodeAccount,
		in.TicketID,
		bucketDom,
		classDom,
		string(ordBuf[:]),
		in.ArtifactKey,
		in.DerivationInputHash,
		in.CompoundProofHashHex,
		string(obsCount[:]),
	)
	parts = append(parts, observers...)
	parts = append(parts, domainTranscript)

	return hex.EncodeToString(storageTruthAssignmentHash(epochSeed[:], parts...)), nil
}

// compareBytes is bytes.Compare; inlined to avoid the bytes import for a
// single call site.
func compareBytes(a, b []byte) int {
	la, lb := len(a), len(b)
	n := la
	if lb < n {
		n = lb
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	default:
		return 0
	}
}
