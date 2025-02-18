package handshake

import (
	"testing"

	"github.com/stretchr/testify/assert"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
)

func TestNewAuthInfo(t *testing.T) {
	autInfo := NewAuthInfo(ClientSide, securekeyx.Simplenode, "cosmos1")
	assert.NotNil(t, autInfo, "AuthInfo should be initialized")
	assert.Equal(t, LumeraALTSProtocol, autInfo.AuthType(), "AuthType should match")
	assert.Equal(t, ClientSide, autInfo.(*AuthInfo).Side, "Side should match")
	assert.Equal(t, securekeyx.Simplenode, autInfo.(*AuthInfo).RemotePeerType, "RemotePeerType should match")
	assert.Equal(t, "cosmos1", autInfo.(*AuthInfo).RemoteIdentity, "RemoteIdentity should match")
}