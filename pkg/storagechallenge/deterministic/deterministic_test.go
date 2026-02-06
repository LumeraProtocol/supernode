package deterministic

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectChallengers_Deterministic(t *testing.T) {
	seed, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	require.NoError(t, err)

	active := []string{
		"lumera1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"lumera1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"lumera1cccccccccccccccccccccccccccccccccccccc",
		"lumera1dddddddddddddddddddddddddddddddddddddd",
		"lumera1eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"lumera1ffffffffffffffffffffffffffffffffffffff",
	}

	c1 := SelectChallengers(active, seed, 7, 0)
	c2 := SelectChallengers(active, seed, 7, 0)
	require.Equal(t, c1, c2)

	// auto=ceil(6/3)=2
	require.Len(t, c1, 2)
	require.NotEqual(t, c1[0], c1[1])
}

func TestSelectFileKeys_NoDuplicates(t *testing.T) {
	seed, err := hex.DecodeString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	require.NoError(t, err)

	candidates := []string{"k3", "k1", "k2", "k4"}
	out := SelectFileKeys(candidates, seed, 0, "lumera1id", 3)
	require.Len(t, out, 3)

	seen := map[string]struct{}{}
	for _, k := range out {
		_, ok := seen[k]
		require.False(t, ok)
		seen[k] = struct{}{}
	}
}
