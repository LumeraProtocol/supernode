package cascadekit

import (
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	cosmoskeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// BuildCascadeRequest builds a Cascade request metadata from layout and file bytes.
// It computes blake3(data) base64, creates the index signature format and index IDs,
// and returns a CascadeMetadata ready for RequestAction.
func BuildCascadeRequest(layout codec.Layout, fileBytes []byte, fileName string, kr cosmoskeyring.Keyring, keyName string, ic, max uint32, public bool) (actiontypes.CascadeMetadata, []string, error) {
	dataHashB64, err := ComputeBlake3DataHashB64(fileBytes)
	if err != nil {
		return actiontypes.CascadeMetadata{}, nil, err
	}
	indexSignatureFormat, indexIDs, err := CreateSignaturesWithKeyring(layout, kr, keyName, ic, max)
	if err != nil {
		return actiontypes.CascadeMetadata{}, nil, err
	}
	meta := NewCascadeMetadata(dataHashB64, fileName, uint64(ic), indexSignatureFormat, public)
	return meta, indexIDs, nil
}
