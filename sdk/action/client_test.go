package action

import (
	"context"
	"testing"

	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	sdkconfig "github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
)

func TestNewClientUsesLumeraBech32(t *testing.T) {
	kr := newTestKeyring(t)
	rec, _, err := kr.NewMnemonic("test", sdkkeyring.English, snkeyring.DefaultHDPath, snkeyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatalf("new mnemonic: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get address: %v", err)
	}
	lumeraAddr, err := sdk.Bech32ifyAddressBytes(snkeyring.AccountAddressPrefix, addr.Bytes())
	if err != nil {
		t.Fatalf("bech32ify lumera: %v", err)
	}
	cosmosAddr, err := sdk.Bech32ifyAddressBytes("cosmos", addr.Bytes())
	if err != nil {
		t.Fatalf("bech32ify cosmos: %v", err)
	}

	origAdapter := newLumeraAdapter
	defer func() { newLumeraAdapter = origAdapter }()
	newLumeraAdapter = func(ctx context.Context, cfg lumera.ConfigParams, logger log.Logger) (lumera.Client, error) {
		return nil, nil
	}

	cfg := sdkconfig.Config{
		Account: sdkconfig.AccountConfig{KeyName: "test", Keyring: kr},
		Lumera:  sdkconfig.LumeraConfig{GRPCAddr: "127.0.0.1:1", ChainID: "lumera-test"},
	}
	client, err := NewClient(context.Background(), cfg, log.NewNoopLogger())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	impl, ok := client.(*ClientImpl)
	if !ok {
		t.Fatalf("expected *ClientImpl, got %T", client)
	}
	if impl.signerAddr != lumeraAddr {
		t.Fatalf("signer address mismatch: got %s want %s", impl.signerAddr, lumeraAddr)
	}
	if impl.signerAddr == cosmosAddr {
		t.Fatalf("signer address not normalized: got %s", impl.signerAddr)
	}
}

func newTestKeyring(t *testing.T) sdkkeyring.Keyring {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	return sdkkeyring.NewInMemory(cdc)
}
