package cascade

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LumeraProtocol/supernode/pkg/codec"
	"github.com/LumeraProtocol/supernode/pkg/utils"
	"github.com/stretchr/testify/assert"
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

func Test_verifyIDs(t *testing.T) {
	tests := []struct {
		name      string
		ticket    codec.Layout
		metadata  codec.Layout
		expectErr string
	}{
		{
			name: "success match",
			ticket: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"A"}, Hash: "abc"},
			}},
			metadata: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"A"}, Hash: "abc"},
			}},
		},
		{
			name: "symbol mismatch",
			ticket: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"A"}},
			}},
			metadata: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"B"}},
			}},
			expectErr: "symbol identifiers don't match",
		},
		{
			name: "hash mismatch",
			ticket: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"A"}, Hash: "a"},
			}},
			metadata: codec.Layout{Blocks: []codec.Block{
				{Symbols: []string{"A"}, Hash: "b"},
			}},
			expectErr: "block hashes don't match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyIDs(context.Background(), tt.ticket, tt.metadata)
			if tt.expectErr != "" {
				assert.ErrorContains(t, err, tt.expectErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
