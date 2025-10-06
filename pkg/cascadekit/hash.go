package cascadekit

import (
	"bytes"
	"encoding/base64"
	"io"

	"lukechampine.com/blake3"
)

// ComputeBlake3Hash computes a 32-byte Blake3 hash of the given data.
func ComputeBlake3Hash(msg []byte) ([]byte, error) {
	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, bytes.NewReader(msg)); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

// ComputeBlake3DataHashB64 computes a Blake3 hash of the input and
// returns it as a base64-encoded string.
func ComputeBlake3DataHashB64(data []byte) (string, error) {
	h, err := ComputeBlake3Hash(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h), nil
}
