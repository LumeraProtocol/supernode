package verifier

import (
	"net"
	"strconv"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/supernode/config"
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
				Valid:  true,
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

func TestConfigVerifier_isPortAvailable(t *testing.T) {
	cfg := &config.Config{
		SupernodeConfig: config.SupernodeConfig{
			Identity: "lumera1testaddress",
			KeyName:  "test-key",
			Host:     "127.0.0.1",
		},
	}

	verifier := NewConfigVerifier(cfg, nil, nil).(*ConfigVerifier)

	// Test available port
	available := verifier.isPortAvailable("127.0.0.1", 0) // Port 0 lets OS choose available port
	assert.True(t, available)

	// Test unavailable port by creating a listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	// Extract the port that was assigned
	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	assert.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	assert.NoError(t, err)

	// Now test that this port is not available
	available = verifier.isPortAvailable("127.0.0.1", port)
	assert.False(t, available)
}

func TestConfigVerifier_checkPortsAvailable(t *testing.T) {
	// Create a listener to occupy a port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer listener.Close()

	// Extract the port that was assigned
	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	assert.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	assert.NoError(t, err)

	cfg := &config.Config{
		SupernodeConfig: config.SupernodeConfig{
			Identity: "lumera1testaddress",
			KeyName:  "test-key",
			Host:     "127.0.0.1",
			Port:     uint16(port), // Use the occupied port
		},
		P2PConfig: config.P2PConfig{
			Port: 0, // Available port
		},
	}

	verifier := NewConfigVerifier(cfg, nil, nil).(*ConfigVerifier)
	result := &VerificationResult{
		Valid:    true,
		Errors:   []ConfigError{},
		Warnings: []ConfigError{},
	}

	verifier.checkPortsAvailable(result)

	// Should have error for supernode port being unavailable
	assert.False(t, result.IsValid())
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, "supernode_port", result.Errors[0].Field)
	assert.Contains(t, result.Errors[0].Message, "already in use")
}

func TestConfigVerifier_checkPortsAvailable_DefaultGatewayPort(t *testing.T) {
	// Create a listener to occupy the default gateway port 8002
	listener, err := net.Listen("tcp", "127.0.0.1:8002")
	assert.NoError(t, err)
	defer listener.Close()

	cfg := &config.Config{
		SupernodeConfig: config.SupernodeConfig{
			Identity:    "lumera1testaddress",
			KeyName:     "test-key",
			Host:        "127.0.0.1",
			Port:        4444, // Available port
			GatewayPort: 0,    // Not configured, should use default 8002
		},
		P2PConfig: config.P2PConfig{
			Port: 4445, // Available port
		},
	}

	verifier := NewConfigVerifier(cfg, nil, nil).(*ConfigVerifier)
	result := &VerificationResult{
		Valid:    true,
		Errors:   []ConfigError{},
		Warnings: []ConfigError{},
	}

	verifier.checkPortsAvailable(result)

	// Should have error for default gateway port being unavailable
	assert.False(t, result.IsValid())
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, "gateway_port", result.Errors[0].Field)
	assert.Equal(t, "8002", result.Errors[0].Actual)
	assert.Contains(t, result.Errors[0].Message, "Port 8002 is already in use")
}
