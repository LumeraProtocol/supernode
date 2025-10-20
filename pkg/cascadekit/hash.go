package cascadekit

import (
	"encoding/base64"

	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// ComputeBlake3DataHashB64 computes a Blake3 hash of the input and
// returns it as a base64-encoded string.
func ComputeBlake3DataHashB64(data []byte) (string, error) {
	h, err := utils.Blake3Hash(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h), nil
}
