package credentials

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// LumeraAddress represents the components of a Lumera address
type LumeraAddress struct {
	Identity string
	Host     string
	Port     uint16
}

type LumeraAddresses []LumeraAddress

// String returns the address in the format "identity@host:port"
func (a LumeraAddress) String() string {
	return fmt.Sprintf("%s@%s:%d", a.Identity, a.Host, a.Port)
}

// HostPort returns just the "host:port" portion
func (a LumeraAddress) HostPort() string {
	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

// ExtractIdentity extracts the identity part from an address in the format "identity@address"
// Returns the identity and the standard address
// If requireIdentity is true, an error is returned when identity is not found
func ExtractIdentity(address string, requireIdentity ...bool) (string, string, error) {
	parts := strings.SplitN(address, "@", 2)

	// Check if identity is required
	identityRequired := false
	if len(requireIdentity) > 0 {
		identityRequired = requireIdentity[0]
	}

	if len(parts) != 2 {
		// Not in Lumera format, return empty identity and original address
		if identityRequired {
			return "", "", fmt.Errorf("identity required but not found in address: %s", address)
		}
		return "", address, nil
	}

	identity := parts[0]
	standardAddress := parts[1]

	if identity == "" {
		return "", "", fmt.Errorf("empty identity found in address: %s", address)
	}

	if standardAddress == "" {
		return "", "", fmt.Errorf("missing address in: %s", address)
	}

	return identity, standardAddress, nil
}

// ParseLumeraAddress parses a Lumera address in the format "identity@host:port"
// and returns the components as a LumeraAddress struct.
// Returns an error if any component, including the port, is missing.
func ParseLumeraAddress(address string) (LumeraAddress, error) {
	var result LumeraAddress

	// Extract identity and the remainder (host:port)
	identity, remainder, err := ExtractIdentity(address, true)
	if err != nil {
		return result, fmt.Errorf("failed to extract identity: %w", err)
	}
	result.Identity = identity

	// Split the remainder into host and port
	host, portStr, err := net.SplitHostPort(remainder)
	if err != nil {
		// If port is missing or any other format error, return an error
		return result, fmt.Errorf("invalid host:port format: %w", err)
	}
	if host == "" {
		return result, fmt.Errorf("missing host in address: %s", address)
	}

	result.Host = host

	// Parse the port string to uint16
	portInt, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return result, fmt.Errorf("invalid port number: %w", err)
	}
	result.Port = uint16(portInt)

	return result, nil
}

// IsLumeraAddressFormat checks if the address is in Lumera format (contains @)
func IsLumeraAddressFormat(address string) bool {
	return strings.Contains(address, "@")
}

// FormatAddressWithIdentity creates a properly formatted address with identity@address
func FormatAddressWithIdentity(identity, address string) string {
	return fmt.Sprintf("%s@%s", identity, address)
}
