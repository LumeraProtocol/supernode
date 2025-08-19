package cascade

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/cosmos/btcutil/base58"
	"github.com/stretchr/testify/assert"
)

func TestGenRQIdentifiersFiles(t *testing.T) {
	tests := []struct {
		name          string
		req           GenRQIdentifiersFilesRequest
		expectedCount int
	}{
		{
			name: "basic valid request",
			req: GenRQIdentifiersFilesRequest{
				Metadata: codec.Layout{
					Blocks: []codec.Block{
						{
							BlockID:           1,
							EncoderParameters: []int{1, 2},
							OriginalOffset:    0,
							Size:              10,
							Symbols:           []string{"s1", "s2"},
							Hash:              "abcd1234",
						},
					},
				},
				Signature: "sig",
				RqMax:     2,
				IC:        1,
			},
			expectedCount: 2,
		},
		{
			name: "different IC value",
			req: GenRQIdentifiersFilesRequest{
				Metadata: codec.Layout{
					Blocks: []codec.Block{
						{
							BlockID:           5,
							EncoderParameters: []int{9},
							OriginalOffset:    99,
							Size:              42,
							Symbols:           []string{"x"},
							Hash:              "z",
						},
					},
				},
				Signature: "mysig",
				RqMax:     1,
				IC:        5,
			},
			expectedCount: 1,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := GenRQIdentifiersFiles(ctx, tt.req)
			assert.NoError(t, err)
			assert.Len(t, resp.RQIDs, tt.expectedCount)
			assert.Len(t, resp.RedundantMetadataFiles, tt.expectedCount)

			// independently compute expected response
			metadataBytes, err := json.Marshal(tt.req.Metadata)
			assert.NoError(t, err)

			base64Meta := utils.B64Encode(metadataBytes)

			for i := 0; i < tt.expectedCount; i++ {
				composite := append(base64Meta, []byte(fmt.Sprintf(".%s.%d", tt.req.Signature, tt.req.IC+uint32(i)))...)
				compressed, err := utils.ZstdCompress(composite)
				assert.NoError(t, err)

				hash, err := utils.Blake3Hash(compressed)
				assert.NoError(t, err)

				expectedRQID := base58.Encode(hash)

				assert.Equal(t, expectedRQID, resp.RQIDs[i])
				assert.Equal(t, compressed, resp.RedundantMetadataFiles[i])
			}
		})
	}
}
