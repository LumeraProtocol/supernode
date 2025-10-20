package cascadekit

import (
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	keyringpkg "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// CreateSignaturesWithKeyring signs layout and index using a Cosmos keyring.
// These helpers centralize keyring-backed signing for clarity.
func CreateSignaturesWithKeyring(layout codec.Layout, kr cosmoskeyring.Keyring, keyName string, ic, max uint32) (string, []string, error) {
	signer := func(msg []byte) ([]byte, error) { return keyringpkg.SignBytes(kr, keyName, msg) }
	return CreateSignatures(layout, signer, ic, max)
}
