package storage_challenge

import (
	"context"
	"testing"

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
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

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
