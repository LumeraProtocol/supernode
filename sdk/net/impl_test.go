package net

import (
	"context"
	"fmt"
	"testing"

	snkeyring "github.com/LumeraProtocol/supernode/v2/pkg/keyring"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	grpcCreds "google.golang.org/grpc/credentials"
)

func TestNewSupernodeClientUsesLumeraBech32(t *testing.T) {
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

	origCreds := newClientCreds
	defer func() { newClientCreds = origCreds }()
	var got string
	newClientCreds = func(opts *ltc.ClientOptions) (grpcCreds.TransportCredentials, error) {
		got = opts.CommonOptions.LocalIdentity
		return nil, fmt.Errorf("stub creds error")
	}

	_, err = NewSupernodeClient(context.Background(), log.NewNoopLogger(), kr, FactoryConfig{KeyName: "test"}, lumera.Supernode{}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got == "" {
		t.Fatal("expected local identity to be captured")
	}
	if got != expected {
		t.Fatalf("local identity mismatch: got %s want %s", got, expected)
	}
}
