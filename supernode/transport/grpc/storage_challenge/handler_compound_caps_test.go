package storage_challenge

import (
	"context"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGetCompoundProof_RangeCountCap verifies C6: requests with more than
// MaxCompoundRanges ranges are rejected with InvalidArgument before any
// artifact bytes are read.
func TestGetCompoundProof_RangeCountCap(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	rl := uint64(deterministic.LEP6CompoundRangeLenBytes)
	ranges := make([]*supernode.ByteRange, 0, deterministic.MaxCompoundRanges+1)
	for i := uint64(0); i < uint64(deterministic.MaxCompoundRanges)+1; i++ {
		ranges = append(ranges, &supernode.ByteRange{Start: i * 1024, End: i*1024 + rl})
	}
	req := compoundRequestWith(ranges, 1<<20)

	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "too many ranges")
	require.Equal(t, 0, reader.calls, "no bytes should be read on cap rejection")
}

// TestGetCompoundProof_RangeLengthCap verifies C6: a single range whose
// length exceeds MaxCompoundRangeLenBytes is rejected.
func TestGetCompoundProof_RangeLengthCap(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	overlong := uint64(deterministic.MaxCompoundRangeLenBytes) + 1
	req := compoundRequestWith([]*supernode.ByteRange{
		{Start: 0, End: overlong},
	}, 1<<20)

	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "range length")
	require.Equal(t, 0, reader.calls)
}

// TestGetCompoundProof_AggregateBytesCap_DefenseInDepth verifies C6: the
// aggregate-bytes cap is wired and matches MaxCompoundAggregateBytes. With
// the current constants (16 × 1024 = 16384 = MaxCompoundAggregateBytes), the
// individual count/length caps fire first; this test acts as a regression
// guard against constants drifting in a way that lets aggregate exceed the
// declared maximum without per-call rejection.
func TestGetCompoundProof_AggregateBytesCap_DefenseInDepth(t *testing.T) {
	t.Parallel()
	require.LessOrEqualf(t,
		uint64(deterministic.MaxCompoundRanges)*uint64(deterministic.MaxCompoundRangeLenBytes),
		uint64(deterministic.MaxCompoundAggregateBytes),
		"per-range and per-count caps must not permit a request that exceeds MaxCompoundAggregateBytes")
}

// TestGetCompoundProof_AggregateAtExactCap verifies C6: aggregate exactly
// equal to the cap is accepted (boundary).
func TestGetCompoundProof_AggregateAtExactCap(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).
		WithArtifactReader(reader).
		WithCompoundCapsForTest(uint32(deterministic.MaxCompoundRanges), uint32(deterministic.MaxCompoundRangeLenBytes))

	// 16 ranges × 1024 bytes/range = 16384 bytes = MaxCompoundAggregateBytes exactly.
	rl := uint64(deterministic.MaxCompoundAggregateBytes / deterministic.MaxCompoundRanges)
	require.LessOrEqual(t, rl, uint64(deterministic.MaxCompoundRangeLenBytes))
	ranges := make([]*supernode.ByteRange, 0, deterministic.MaxCompoundRanges)
	for i := uint64(0); i < uint64(deterministic.MaxCompoundRanges); i++ {
		ranges = append(ranges, &supernode.ByteRange{Start: i * 4096, End: i*4096 + rl})
	}
	req := compoundRequestWith(ranges, 1<<20)

	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Ok, "error: %s", resp.Error)
	require.Equal(t, deterministic.MaxCompoundRanges, reader.calls)
}

func TestGetCompoundProofHonorsChainParamCaps(t *testing.T) {
	srv := NewServer("recipient-1", &testP2PClient{}, nil).
		WithArtifactReader(&deterministicReader{}).
		WithCompoundCapsForTest(4, 256)
	req := validCompoundRequestForCaps(5, 128)
	_, err := srv.GetCompoundProof(context.Background(), req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for 5 ranges over chain cap 4, got %v", err)
	}

	req = validCompoundRequestForCaps(4, 257)
	_, err = srv.GetCompoundProof(context.Background(), req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for len 257 over chain cap 256, got %v", err)
	}
}

func validCompoundRequestForCaps(n int, size uint64) *supernode.GetCompoundProofRequest {
	ranges := make([]*supernode.ByteRange, 0, n)
	for i := 0; i < n; i++ {
		start := uint64(i) * size
		ranges = append(ranges, &supernode.ByteRange{Start: start, End: start + size})
	}
	return &supernode.GetCompoundProofRequest{
		ChallengeId:     "challenge-caps",
		EpochId:         7,
		TicketId:        "ticket-caps",
		ArtifactClass:   uint32(audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL),
		ArtifactKey:     "artifact-caps",
		ArtifactSize:    uint64(n)*size + 1,
		BucketType:      uint32(audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT),
		ArtifactOrdinal: 0,
		ArtifactCount:   1,
		Ranges:          ranges,
	}
}

func TestGetCompoundProof_AggregateBytesCapBranch(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).
		WithArtifactReader(reader).
		WithCompoundCapsForTest(20, 1024)

	// Each range is within the chain-param count/length caps, but the total
	// payload exceeds MaxCompoundAggregateBytes so the aggregate guard itself
	// must reject before any artifact bytes are read.
	rangeLen := uint64(1000)
	ranges := make([]*supernode.ByteRange, 0, 17)
	for i := uint64(0); i < 17; i++ {
		ranges = append(ranges, &supernode.ByteRange{Start: i * (1 << 20), End: i*(1<<20) + rangeLen})
	}
	req := compoundRequestWith(ranges, 1<<30)

	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.Error(t, err)
	require.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Contains(t, st.Message(), "aggregate")
	require.Equal(t, 0, reader.calls)
}
