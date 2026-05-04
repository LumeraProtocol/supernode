package storage_challenge

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/gen/supernode"
	"github.com/LumeraProtocol/supernode/v2/pkg/storagechallenge/deterministic"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/require"
	"lukechampine.com/blake3"
)

// deterministicReader produces reproducible bytes derived from
// (class, key, start, end) so tests can assert exact proof hashes.
type deterministicReader struct {
	calls int
	err   error
}

func (r *deterministicReader) ReadArtifactRange(_ context.Context, class audittypes.StorageProofArtifactClass, key string, start, end uint64) ([]byte, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	out := make([]byte, end-start)
	seed := make([]byte, 0, 32+len(key))
	var sb [4]byte
	binary.BigEndian.PutUint32(sb[:], uint32(class))
	seed = append(seed, sb[:]...)
	seed = append(seed, []byte(key)...)
	var ab [16]byte
	binary.BigEndian.PutUint64(ab[0:8], start)
	binary.BigEndian.PutUint64(ab[8:16], end)
	seed = append(seed, ab[:]...)
	h := blake3.New(int(end-start), nil)
	_, _ = h.Write(seed)
	copy(out, h.Sum(nil))
	return out, nil
}

func compoundRequestWith(ranges []*supernode.ByteRange, artifactSize uint64) *supernode.GetCompoundProofRequest {
	return &supernode.GetCompoundProofRequest{
		ChallengeId:            "challenge-c1",
		EpochId:                42,
		TicketId:               "ticket-1",
		TargetSupernodeAccount: "sn-target",
		ChallengerAccount:      "sn-challenger",
		ObserverAccounts:       []string{"o1", "o2"},
		ArtifactClass:          uint32(audittypes.StorageProofArtifactClass_STORAGE_PROOF_ARTIFACT_CLASS_SYMBOL),
		ArtifactOrdinal:        3,
		ArtifactCount:          8,
		BucketType:             uint32(audittypes.StorageProofBucketType_STORAGE_PROOF_BUCKET_TYPE_RECENT),
		ArtifactKey:            "artifact-key-1",
		ArtifactSize:           artifactSize,
		Ranges:                 ranges,
	}
}

func fourValidRanges() []*supernode.ByteRange {
	rl := uint64(deterministic.LEP6CompoundRangeLenBytes)
	return []*supernode.ByteRange{
		{Start: 0, End: rl},
		{Start: 1024, End: 1024 + rl},
		{Start: 4096, End: 4096 + rl},
		{Start: 8192, End: 8192 + rl},
	}
}

func newCompoundProofKeyring(t *testing.T) (keyring.Keyring, string) {
	t.Helper()
	ir := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(ir)
	cdc := codec.NewProtoCodec(ir)
	kr := keyring.NewInMemory(cdc)
	entropy, err := bip39.NewEntropy(128)
	require.NoError(t, err)
	mnemonic, err := bip39.NewMnemonic(entropy)
	require.NoError(t, err)
	algos, _ := kr.SupportedAlgorithms()
	algo, err := keyring.NewSigningAlgoFromString("secp256k1", algos)
	require.NoError(t, err)
	_, err = kr.NewAccount("recipient-test", mnemonic, "", hd.CreateHDPath(118, 0, 0).String(), algo)
	require.NoError(t, err)
	return kr, "recipient-test"
}

func TestGetCompoundProof_HappyPath(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	req := compoundRequestWith(fourValidRanges(), 1<<20)
	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Ok, "error: %s", resp.Error)
	require.Empty(t, resp.Error)
	require.Len(t, resp.RangeBytes, deterministic.LEP6CompoundRangesPerArtifact)
	for i, b := range resp.RangeBytes {
		require.Lenf(t, b, deterministic.LEP6CompoundRangeLenBytes, "range[%d]", i)
	}
	require.Equal(t, deterministic.LEP6CompoundRangesPerArtifact, reader.calls)

	// Recompute expected hash via the same deterministic reader to compare.
	h := blake3.New(32, nil)
	for _, b := range resp.RangeBytes {
		_, _ = h.Write(b)
	}
	require.Equal(t, hex.EncodeToString(h.Sum(nil)), resp.ProofHashHex)
	require.Empty(t, resp.RecipientSignature, "recipient signature deferred to B.3")
	require.Equal(t, req.ChallengeId, resp.ChallengeId)
	require.Equal(t, req.TicketId, resp.TicketId)
	require.Equal(t, req.ArtifactKey, resp.ArtifactKey)
}

func TestGetCompoundProof_RecipientSignatureUsesDerivationInputHash(t *testing.T) {
	reader := &deterministicReader{}
	kr, keyName := newCompoundProofKeyring(t)
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader).WithRecipientSigner(kr, keyName)

	req := compoundRequestWith(fourValidRanges(), 1<<20)
	req.Seed = []byte("0123456789abcdef0123456789abcdef")
	resp, err := srv.GetCompoundProof(context.Background(), req)
	require.NoError(t, err)
	require.True(t, resp.Ok, "error: %s", resp.Error)
	require.NotEmpty(t, resp.RecipientSignature)

	offsets := make([]uint64, 0, len(req.Ranges))
	for _, rng := range req.Ranges {
		offsets = append(offsets, rng.Start)
	}
	class := audittypes.StorageProofArtifactClass(req.ArtifactClass)
	derivHash, err := deterministic.DerivationInputHash(req.Seed, req.TargetSupernodeAccount, req.TicketId, class, req.ArtifactOrdinal, offsets, uint64(deterministic.LEP6CompoundRangeLenBytes))
	require.NoError(t, err)
	expectedTranscript, err := deterministic.TranscriptHash(deterministic.TranscriptInputs{
		EpochID:                    req.EpochId,
		ChallengerSupernodeAccount: req.ChallengerAccount,
		TargetSupernodeAccount:     req.TargetSupernodeAccount,
		TicketID:                   req.TicketId,
		Bucket:                     audittypes.StorageProofBucketType(req.BucketType),
		ArtifactClass:              class,
		ArtifactOrdinal:            req.ArtifactOrdinal,
		ArtifactKey:                req.ArtifactKey,
		DerivationInputHash:        derivHash,
		CompoundProofHashHex:       resp.ProofHashHex,
		ObserverIDs:                req.ObserverAccounts,
	})
	require.NoError(t, err)

	emptyDerivTranscript, err := deterministic.TranscriptHash(deterministic.TranscriptInputs{
		EpochID:                    req.EpochId,
		ChallengerSupernodeAccount: req.ChallengerAccount,
		TargetSupernodeAccount:     req.TargetSupernodeAccount,
		TicketID:                   req.TicketId,
		Bucket:                     audittypes.StorageProofBucketType(req.BucketType),
		ArtifactClass:              class,
		ArtifactOrdinal:            req.ArtifactOrdinal,
		ArtifactKey:                req.ArtifactKey,
		DerivationInputHash:        "",
		CompoundProofHashHex:       resp.ProofHashHex,
		ObserverIDs:                req.ObserverAccounts,
	})
	require.NoError(t, err)

	sig, err := hex.DecodeString(resp.RecipientSignature)
	require.NoError(t, err)
	rec, err := kr.Key(keyName)
	require.NoError(t, err)
	pub, err := rec.GetPubKey()
	require.NoError(t, err)
	require.True(t, pub.VerifySignature([]byte(expectedTranscript), sig), "recipient signature must verify against transcript containing derivation hash")
	require.False(t, pub.VerifySignature([]byte(emptyDerivTranscript), sig), "recipient signature must not verify against empty-derivation transcript")
}

func TestGetCompoundProof_AcceptsChainParamRangeCount(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	rng := fourValidRanges()[:3]
	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(rng, 1<<20))
	require.NoError(t, err)
	require.True(t, resp.Ok, "error: %s", resp.Error)
	require.Len(t, resp.RangeBytes, 3)
	require.Equal(t, 3, reader.calls)
}

func TestGetCompoundProof_AcceptsChainParamRangeSize(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	ranges := []*supernode.ByteRange{
		{Start: 0, End: 200},
		{Start: 1024, End: 1224},
		{Start: 4096, End: 4296},
		{Start: 8192, End: 8392},
	}
	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(ranges, 1<<20))
	require.NoError(t, err)
	require.True(t, resp.Ok, "error: %s", resp.Error)
	require.Len(t, resp.RangeBytes, len(ranges))
	for i, b := range resp.RangeBytes {
		require.Lenf(t, b, 200, "range[%d]", i)
	}
	require.Equal(t, len(ranges), reader.calls)
}

func TestGetCompoundProof_RejectsInconsistentRangeSize(t *testing.T) {
	t.Parallel()

	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(&deterministicReader{})

	bad := []*supernode.ByteRange{
		{Start: 0, End: 200},
		{Start: 1024, End: 1225},
	}
	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(bad, 1<<20))
	require.NoError(t, err)
	require.False(t, resp.Ok)
	require.Contains(t, resp.Error, "invalid size")
	require.Empty(t, resp.RangeBytes)
}

func TestGetCompoundProof_RejectsEmptyRanges(t *testing.T) {
	t.Parallel()

	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(&deterministicReader{})

	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(nil, 1<<20))
	require.NoError(t, err)
	require.False(t, resp.Ok)
	require.Contains(t, resp.Error, "at least one range")
	require.Empty(t, resp.RangeBytes)
}

func TestGetCompoundProof_RangeOutOfBounds(t *testing.T) {
	t.Parallel()

	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(&deterministicReader{})

	rl := uint64(deterministic.LEP6CompoundRangeLenBytes)
	rs := fourValidRanges()
	// last range straddles end of artifact
	artifactSize := rs[3].End - 1
	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(rs, artifactSize))
	require.NoError(t, err)
	require.False(t, resp.Ok)
	require.Contains(t, resp.Error, "out of bounds")
	require.Empty(t, resp.RangeBytes)
	_ = rl
}

func TestGetCompoundProof_ReaderError(t *testing.T) {
	t.Parallel()

	reader := &deterministicReader{err: io.ErrUnexpectedEOF}
	srv := NewServer("recipient-1", &testP2PClient{}, nil).WithArtifactReader(reader)

	resp, err := srv.GetCompoundProof(context.Background(), compoundRequestWith(fourValidRanges(), 1<<20))
	require.NoError(t, err)
	require.False(t, resp.Ok)
	require.True(t, strings.Contains(resp.Error, io.ErrUnexpectedEOF.Error()), "error %q must wrap %v", resp.Error, io.ErrUnexpectedEOF)
	require.Empty(t, resp.RangeBytes)
}
