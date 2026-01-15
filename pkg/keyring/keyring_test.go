package keyring

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestGetBech32Address(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	kr := sdkkeyring.NewInMemory(cdc)

	rec, _, err := kr.NewMnemonic("test", sdkkeyring.English, DefaultHDPath, DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatalf("new mnemonic: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get address: %v", err)
	}

	got, err := GetBech32Address(kr, "test", "lumera")
	if err != nil {
		t.Fatalf("get bech32 address: %v", err)
	}
	if got == "" {
		t.Fatal("empty bech32 address")
	}
	bz, err := sdk.GetFromBech32(got, "lumera")
	if err != nil {
		t.Fatalf("decode bech32: %v", err)
	}
	if !bytes.Equal(bz, addr.Bytes()) {
		t.Fatalf("decoded address mismatch")
	}
}
