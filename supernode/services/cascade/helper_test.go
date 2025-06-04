package cascade

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_extractSignatureAndFirstPart(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		sig      string
		hasErr   bool
	}{
		{"valid format", "data.sig", "data", "sig", false},
		{"no dot", "nodelimiter", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, sig, err := extractSignatureAndFirstPart(tt.input)
			if tt.hasErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, data)
				assert.Equal(t, tt.sig, sig)
			}
		})
	}
}

func Test_decodeMetadataFile(t *testing.T) {
	layout := codec.Layout{
		Blocks: []codec.Block{{BlockID: 1, Hash: "abc", Symbols: []string{"s"}}},
	}
	jsonBytes, _ := json.Marshal(layout)
	encoded := utils.B64Encode(jsonBytes)

	tests := []struct {
		name      string
		input     string
		expectErr bool
		wantHash  string
	}{
		{"valid base64+json", string(encoded), false, "abc"},
		{"invalid base64", "!@#$%", true, ""},
		{"bad json", string(utils.B64Encode([]byte("{broken"))), true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := decodeMetadataFile(tt.input)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantHash, out.Blocks[0].Hash)
			}
		})
	}
}

func Test_verifyLayoutHash(t *testing.T) {
	// Create a sample layout
	layout := codec.Layout{
		Blocks: []codec.Block{
			{
				BlockID: 1,
				Hash:    "sample_hash",
				Symbols: []string{"symbol1", "symbol2"},
			},
		},
	}

	// Marshal to JSON and calculate the Blake3 hash
	jsonBytes, err := json.Marshal(layout)
	require.NoError(t, err)

	blake3Hash, err := utils.Blake3Hash(jsonBytes)
	require.NoError(t, err)

	validB64Hash := base64.StdEncoding.EncodeToString(blake3Hash)

	tests := []struct {
		name              string
		b64EncodedHash    string
		metadata          codec.Layout
		expectErr         bool
		expectedErrSubstr string
	}{
		{
			name:           "valid hash match",
			b64EncodedHash: validB64Hash,
			metadata:       layout,
			expectErr:      false,
		},
		{
			name:              "hash mismatch",
			b64EncodedHash:    "invalid_hash_that_wont_match",
			metadata:          layout,
			expectErr:         true,
			expectedErrSubstr: "layout hash mismatch",
		},
		{
			name:           "different metadata",
			b64EncodedHash: validB64Hash,
			metadata: codec.Layout{
				Blocks: []codec.Block{
					{
						BlockID: 2,
						Hash:    "different_hash",
						Symbols: []string{"different_symbol"},
					},
				},
			},
			expectErr:         true,
			expectedErrSubstr: "layout hash mismatch",
		},
		{
			name:              "empty hash",
			b64EncodedHash:    "",
			metadata:          layout,
			expectErr:         true,
			expectedErrSubstr: "layout hash mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectedErrSubstr != "" {
					assert.Contains(t, err.Error(), tt.expectedErrSubstr)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
