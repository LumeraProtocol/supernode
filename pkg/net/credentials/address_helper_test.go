package credentials

import (
	"reflect"
	"testing"
)

func TestExtractIdentity(t *testing.T) {
	tests := []struct {
		name            string
		address         string
		requireIdentity bool
		wantIdentity    string
		wantAddress     string
		wantErr         bool
	}{
		{
			name:            "Valid Lumera address",
			address:         "lumera1abc123@example.com:9090",
			requireIdentity: false,
			wantIdentity:    "lumera1abc123",
			wantAddress:     "example.com:9090",
			wantErr:         false,
		},
		{
			name:            "Valid Lumera address with requireIdentity",
			address:         "lumera1abc123@example.com:9090",
			requireIdentity: true,
			wantIdentity:    "lumera1abc123",
			wantAddress:     "example.com:9090",
			wantErr:         false,
		},
		{
			name:            "Standard address without identity",
			address:         "example.com:9090",
			requireIdentity: false,
			wantIdentity:    "",
			wantAddress:     "example.com:9090",
			wantErr:         false,
		},
		{
			name:            "Standard address with requireIdentity",
			address:         "example.com:9090",
			requireIdentity: true,
			wantIdentity:    "",
			wantAddress:     "",
			wantErr:         true,
		},
		{
			name:            "Empty identity",
			address:         "@example.com:9090",
			requireIdentity: false,
			wantIdentity:    "",
			wantAddress:     "",
			wantErr:         true,
		},
		{
			name:            "Empty address",
			address:         "lumera1abc123@",
			requireIdentity: false,
			wantIdentity:    "",
			wantAddress:     "",
			wantErr:         true,
		},
		{
			name:            "Empty input",
			address:         "",
			requireIdentity: false,
			wantIdentity:    "",
			wantAddress:     "",
			wantErr:         false,
		},
		{
			name:            "Empty input with requireIdentity",
			address:         "",
			requireIdentity: true,
			wantIdentity:    "",
			wantAddress:     "",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIdentity, gotAddress, err := ExtractIdentity(tt.address, tt.requireIdentity)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractIdentity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotIdentity != tt.wantIdentity {
				t.Errorf("ExtractIdentity() got identity = %v, want %v", gotIdentity, tt.wantIdentity)
			}
			if gotAddress != tt.wantAddress {
				t.Errorf("ExtractIdentity() got address = %v, want %v", gotAddress, tt.wantAddress)
			}
		})
	}
}

func TestParseLumeraAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    LumeraAddress
		wantErr bool
	}{
		{
			name:    "Valid address with port",
			address: "lumera1abc123@example.com:9090",
			want: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "example.com",
				Port:     9090,
			},
			wantErr: false,
		},
		{
			name:    "Valid address with IPv4",
			address: "lumera1abc123@192.168.1.1:9090",
			want: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "192.168.1.1",
				Port:     9090,
			},
			wantErr: false,
		},
		{
			name:    "Valid address with IPv6",
			address: "lumera1abc123@[2001:db8::1]:9090",
			want: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "2001:db8::1",
				Port:     9090,
			},
			wantErr: false,
		},
		{
			name:    "Missing port",
			address: "lumera1abc123@example.com",
			want:    LumeraAddress{},
			wantErr: true,
		},
		{
			name:    "Invalid port number",
			address: "lumera1abc123@example.com:abc",
			want:    LumeraAddress{},
			wantErr: true,
		},
		{
			name:    "Port out of range",
			address: "lumera1abc123@example.com:65536",
			want:    LumeraAddress{},
			wantErr: true,
		},
		{
			name:    "Empty host",
			address: "lumera1abc123@:9090",
			want:    LumeraAddress{},
			wantErr: true,
		},
		{
			name:    "No identity",
			address: "example.com:9090",
			want:    LumeraAddress{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLumeraAddress(tt.address)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLumeraAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLumeraAddress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLumeraAddressString(t *testing.T) {
	tests := []struct {
		name    string
		address LumeraAddress
		want    string
	}{
		{
			name: "Normal address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "example.com",
				Port:     9090,
			},
			want: "lumera1abc123@example.com:9090",
		},
		{
			name: "IPv4 address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "192.168.1.1",
				Port:     9090,
			},
			want: "lumera1abc123@192.168.1.1:9090",
		},
		{
			name: "IPv6 address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "2001:db8::1",
				Port:     9090,
			},
			want: "lumera1abc123@2001:db8::1:9090",
		},
		{
			name: "Zero port",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "example.com",
				Port:     0,
			},
			want: "lumera1abc123@example.com:0",
		},
		{
			name: "Empty fields",
			address: LumeraAddress{
				Identity: "",
				Host:     "",
				Port:     0,
			},
			want: "@:0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.address.String()
			if got != tt.want {
				t.Errorf("LumeraAddress.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLumeraAddressHostPort(t *testing.T) {
	tests := []struct {
		name    string
		address LumeraAddress
		want    string
	}{
		{
			name: "Normal address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "example.com",
				Port:     9090,
			},
			want: "example.com:9090",
		},
		{
			name: "IPv4 address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "192.168.1.1",
				Port:     9090,
			},
			want: "192.168.1.1:9090",
		},
		{
			name: "IPv6 address",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "2001:db8::1",
				Port:     9090,
			},
			want: "2001:db8::1:9090",
		},
		{
			name: "Zero port",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "example.com",
				Port:     0,
			},
			want: "example.com:0",
		},
		{
			name: "Empty host",
			address: LumeraAddress{
				Identity: "lumera1abc123",
				Host:     "",
				Port:     9090,
			},
			want: ":9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.address.HostPort()
			if got != tt.want {
				t.Errorf("LumeraAddress.HostPort() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLumeraAddressFormat(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{
			name:    "Lumera format",
			address: "lumera1abc123@example.com:9090",
			want:    true,
		},
		{
			name:    "Standard format",
			address: "example.com:9090",
			want:    false,
		},
		{
			name:    "Empty string",
			address: "",
			want:    false,
		},
		{
			name:    "Contains @ in host",
			address: "user@example.com:9090",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLumeraAddressFormat(tt.address); got != tt.want {
				t.Errorf("IsLumeraAddressFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatAddressWithIdentity(t *testing.T) {
	tests := []struct {
		name     string
		identity string
		address  string
		want     string
	}{
		{
			name:     "Normal inputs",
			identity: "lumera1abc123",
			address:  "example.com:9090",
			want:     "lumera1abc123@example.com:9090",
		},
		{
			name:     "Empty identity",
			identity: "",
			address:  "example.com:9090",
			want:     "@example.com:9090",
		},
		{
			name:     "Empty address",
			identity: "lumera1abc123",
			address:  "",
			want:     "lumera1abc123@",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAddressWithIdentity(tt.identity, tt.address)
			if got != tt.want {
				t.Errorf("FormatAddressWithIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}
