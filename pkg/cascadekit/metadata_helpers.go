package cascadekit

import (
    actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
    "github.com/LumeraProtocol/supernode/v2/pkg/errors"
    "github.com/LumeraProtocol/supernode/v2/pkg/utils"
    "github.com/golang/protobuf/proto"
)

// UnmarshalCascadeMetadata decodes action metadata bytes into CascadeMetadata.
func UnmarshalCascadeMetadata(raw []byte) (actiontypes.CascadeMetadata, error) {
    var meta actiontypes.CascadeMetadata
    if err := proto.Unmarshal(raw, &meta); err != nil {
        return meta, errors.Errorf("failed to unmarshal cascade metadata: %w", err)
    }
    return meta, nil
}

// VerifyB64DataHash compares a raw hash with an expected base64 string.
func VerifyB64DataHash(raw []byte, expectedB64 string) error {
    b64 := utils.B64Encode(raw)
    if string(b64) != expectedB64 {
        return errors.New("data hash doesn't match")
    }
    return nil
}

