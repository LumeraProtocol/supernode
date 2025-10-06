package cascadekit

import (
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	keyringpkg "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// CreateSignaturesWithKeyring signs layout and index using a Cosmos keyring.
func CreateSignaturesWithKeyring(layout codec.Layout, kr cosmoskeyring.Keyring, keyName string, ic, max uint32) (string, []string, error) {
	signer := func(msg []byte) ([]byte, error) { return keyringpkg.SignBytes(kr, keyName, msg) }
	return CreateSignatures(layout, signer, ic, max)
}

// BuildCascadeRequest builds a Cascade request metadata from layout and file bytes.
// It computes blake3(data) base64, creates the signatures string and index IDs,
// and returns a CascadeMetadata ready for RequestAction.
func BuildCascadeRequest(layout codec.Layout, fileBytes []byte, fileName string, kr cosmoskeyring.Keyring, keyName string, ic, max uint32, public bool) (actiontypes.CascadeMetadata, []string, error) {
	dataHashB64, err := ComputeBlake3DataHashB64(fileBytes)
	if err != nil {
		return actiontypes.CascadeMetadata{}, nil, err
	}
	signatures, indexIDs, err := CreateSignaturesWithKeyring(layout, kr, keyName, ic, max)
	if err != nil {
		return actiontypes.CascadeMetadata{}, nil, err
	}
	meta := NewCascadeMetadata(dataHashB64, fileName, uint64(ic), signatures, public)
	return meta, indexIDs, nil
}
