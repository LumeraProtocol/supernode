package system

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/cascade"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// createCascadeLayoutSignature is a wrapper for the common cascade signature function
func createCascadeLayoutSignature(metadataFile codec.Layout, kr cosmoskeyring.Keyring, userKeyName string, ic uint32, maxFiles uint32) (signatureFormat string, indexFileIDs []string, err error) {
	return cascade.CreateLayoutSignature(metadataFile, kr, userKeyName, ic, maxFiles)
}

// ComputeBlake3Hash is a wrapper for the common Blake3 hash function
func ComputeBlake3Hash(msg []byte) ([]byte, error) {
	return cascade.ComputeBlake3Hash(msg)
}