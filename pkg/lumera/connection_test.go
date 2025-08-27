package lumera

import (
	"testing"
	"time"
)

func TestNormaliseAddr(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedHost   string
		expectedTLS    bool
		expectedServer string
		expectError    bool
	}{
		{
			name:           "https scheme",
			input:          "https://grpc.testnet.lumera.io",
			expectedHost:   "grpc.testnet.lumera.io:443",
			expectedTLS:    true,
			expectedServer: "grpc.testnet.lumera.io",
			expectError:    false,
		},
		{
			name:           "grpcs scheme with port",
			input:          "grpcs://grpc.node9x.com:7443",
			expectedHost:   "grpc.node9x.com:7443",
			expectedTLS:    true,
			expectedServer: "grpc.node9x.com",
			expectError:    false,
		},
		{
			name:           "host with port 443",
			input:          "grpc.node9x.com:443",
			expectedHost:   "grpc.node9x.com:443",
			expectedTLS:    true,
			expectedServer: "grpc.node9x.com",
			expectError:    false,
		},
		{
			name:           "host with custom port",
			input:          "grpc.node9x.com:9090",
			expectedHost:   "grpc.node9x.com:9090",
			expectedTLS:    false,
			expectedServer: "grpc.node9x.com",
			expectError:    false,
		},
		{
			name:           "host without port",
			input:          "grpc.testnet.lumera.io",
			expectedHost:   "grpc.testnet.lumera.io:9090",
			expectedTLS:    false,
			expectedServer: "grpc.testnet.lumera.io",
			expectError:    false,
		},
		{
			name:           "invalid scheme",
			input:          "ftp://invalid.com",
			expectedHost:   "",
			expectedTLS:    false,
			expectedServer: "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostPort, useTLS, serverName, err := normaliseAddr(tt.input)

			if tt.expectError && err == nil {
				t.Errorf("normaliseAddr(%s) expected error, got nil", tt.input)
				return
			}

			if !tt.expectError && err != nil {
				t.Errorf("normaliseAddr(%s) unexpected error: %v", tt.input, err)
				return
			}

			if !tt.expectError {
				if hostPort != tt.expectedHost {
					t.Errorf("normaliseAddr(%s) hostPort = %s, want %s", tt.input, hostPort, tt.expectedHost)
				}
				if useTLS != tt.expectedTLS {
					t.Errorf("normaliseAddr(%s) useTLS = %v, want %v", tt.input, useTLS, tt.expectedTLS)
				}
				if serverName != tt.expectedServer {
					t.Errorf("normaliseAddr(%s) serverName = %s, want %s", tt.input, serverName, tt.expectedServer)
				}
			}
		})
	}
}

func TestGrpcConnectionMethods(t *testing.T) {
	// Test with nil connection
	conn := &grpcConnection{conn: nil}

	// Close should not panic with nil connection
	err := conn.Close()
	if err != nil {
		t.Errorf("Close() with nil connection should return nil, got %v", err)
	}

	// GetConn should return nil
	grpcConn := conn.GetConn()
	if grpcConn != nil {
		t.Errorf("GetConn() with nil connection should return nil, got %v", grpcConn)
	}
}

func TestConnectionConstants(t *testing.T) {
	// Test that our constants are reasonable
	if keepaliveTime < 10*time.Second {
		t.Errorf("keepaliveTime too short: %v", keepaliveTime)
	}

	if keepaliveTimeout >= keepaliveTime {
		t.Errorf("keepaliveTimeout should be less than keepaliveTime: %v >= %v", keepaliveTimeout, keepaliveTime)
	}
}

