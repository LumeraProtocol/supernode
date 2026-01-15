package net

import (
	"context"
	"testing"

	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func newTestKeyring(t *testing.T) sdkkeyring.Keyring {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	return sdkkeyring.NewInMemory(cdc)
}

func TestNewClientFactoryUsesLumeraBech32(t *testing.T) {
	kr := newTestKeyring(t)
	rec, _, err := kr.NewMnemonic("test", sdkkeyring.English, snkeyring.DefaultHDPath, snkeyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatalf("new mnemonic: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get address: %v", err)
	}
	expected, err := sdk.Bech32ifyAddressBytes(snkeyring.AccountAddressPrefix, addr.Bytes())
	if err != nil {
		t.Fatalf("bech32ify: %v", err)
	}

	factory, err := NewClientFactory(context.Background(), nil, kr, nil, FactoryConfig{KeyName: "test"})
	if err != nil {
		t.Fatalf("new client factory: %v", err)
	}
	if factory == nil {
		t.Fatal("expected factory")
	}
	if factory.signerAddr != expected {
		t.Fatalf("signer address mismatch: got %s want %s", factory.signerAddr, expected)
	}
}

func TestNewClientFactoryNormalizesNonLumeraPrefix(t *testing.T) {
	kr := newTestKeyring(t)
	rec, _, err := kr.NewMnemonic("test", sdkkeyring.English, snkeyring.DefaultHDPath, snkeyring.DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatalf("new mnemonic: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get address: %v", err)
	}
	cosmosAddr, err := sdk.Bech32ifyAddressBytes("cosmos", addr.Bytes())
	if err != nil {
		t.Fatalf("bech32ify cosmos: %v", err)
	}
	expected, err := sdk.Bech32ifyAddressBytes(snkeyring.AccountAddressPrefix, addr.Bytes())
	if err != nil {
		t.Fatalf("bech32ify lumera: %v", err)
	}

	factory, err := NewClientFactory(context.Background(), nil, kr, nil, FactoryConfig{KeyName: "test"})
	if err != nil {
		t.Fatalf("new client factory: %v", err)
	}
	if factory.signerAddr == cosmosAddr {
		t.Fatalf("signer address was not normalized: got %s", factory.signerAddr)
	}
	if factory.signerAddr != expected {
		t.Fatalf("signer address mismatch: got %s want %s", factory.signerAddr, expected)
	}
}

func TestNewClientFactoryMissingKey(t *testing.T) {
	kr := newTestKeyring(t)
	_, err := NewClientFactory(context.Background(), nil, kr, nil, FactoryConfig{KeyName: "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}
