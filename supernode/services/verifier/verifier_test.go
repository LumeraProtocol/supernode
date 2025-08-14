package verifier

import (
	"testing"

	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/stretchr/testify/assert"
)

func TestNewConfigVerifier(t *testing.T) {
	cfg := &config.Config{
		SupernodeConfig: config.SupernodeConfig{
			Identity: "lumera1testaddress",
			KeyName:  "test-key",
			Host:     "192.168.1.100",
		},
		P2PConfig: config.P2PConfig{
			Port: 4445,
		},
	}

	// Test that NewConfigVerifier returns a non-nil service
	verifier := NewConfigVerifier(cfg, nil, nil)
	assert.NotNil(t, verifier)
	assert.Implements(t, (*ConfigVerifierService)(nil), verifier)
}

func TestVerificationResult_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		result   *VerificationResult
		expected bool
	}{
		{
			name: "valid with no errors",
			result: &VerificationResult{
				Valid:  true,
				Errors: []ConfigError{},
			},
			expected: true,
		},
		{
			name: "invalid with errors",
			result: &VerificationResult{
				Valid: false,
				Errors: []ConfigError{
					{Message: "test error"},
				},
			},
			expected: false,
		},
		{
			name: "valid flag true but has errors",
			result: &VerificationResult{
				Valid: true,
				Errors: []ConfigError{
					{Message: "test error"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.IsValid())
		})
	}
}

func TestVerificationResult_HasWarnings(t *testing.T) {
	tests := []struct {
		name     string
		result   *VerificationResult
		expected bool
	}{
		{
			name: "no warnings",
			result: &VerificationResult{
				Warnings: []ConfigError{},
			},
			expected: false,
		},
		{
			name: "has warnings",
			result: &VerificationResult{
				Warnings: []ConfigError{
					{Message: "test warning"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasWarnings())
		})
	}
}

func TestVerificationResult_Summary(t *testing.T) {
	tests := []struct {
		name     string
		result   *VerificationResult
		contains []string
	}{
		{
			name: "success with no warnings",
			result: &VerificationResult{
				Valid:    true,
				Errors:   []ConfigError{},
				Warnings: []ConfigError{},
			},
			contains: []string{"✓ Config verification successful"},
		},
		{
			name: "error message",
			result: &VerificationResult{
				Valid: false,
				Errors: []ConfigError{
					{
						Message: "Key not found",
					},
				},
			},
			contains: []string{"✗ Key not found"},
		},
		{
			name: "warning message",
			result: &VerificationResult{
				Valid: true,
				Errors: []ConfigError{},
				Warnings: []ConfigError{
					{
						Message: "Host mismatch: config=localhost, chain=192.168.1.1",
					},
				},
			},
			contains: []string{"⚠ Host mismatch: config=localhost, chain=192.168.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := tt.result.Summary()
			for _, expected := range tt.contains {
				assert.Contains(t, summary, expected)
			}
		})
	}
}